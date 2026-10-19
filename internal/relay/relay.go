// SPDX-License-Identifier: GPL-3.0-only
package relay

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"golang.org/x/time/rate"
	"gorm.io/gorm"
)

const maxMessage = 96 * 1024
const defaultBandwidthBitsPerSecond int64 = 1_000_000
const minBandwidthBitsPerSecond int64 = 8_000
const maxBandwidthBitsPerSecond int64 = 100_000_000_000

const (
	// Keep these values aligned with the client receive idle window. A write is
	// sent as several WebSocket fragments so an upstream proxy's per-connection
	// rate limit can make progress without holding one deadline for the whole
	// message.
	websocketReadIdleTimeout  = 75 * time.Second
	websocketWriteIdleTimeout = 75 * time.Second
	websocketPingInterval     = 25 * time.Second
	websocketWriteChunkSize   = 4 * 1024
)

type Device struct {
	ID             string `gorm:"primaryKey;size:24"`
	CredentialHash string `gorm:"uniqueIndex;size:64;not null"`
	CreatedAt      time.Time
	OwnerID        *uint  `gorm:"index"`
	Label          string `gorm:"size:80"`
	Disabled       bool
	LastSeenAt     *time.Time
}

// Body is an opaque, bounded protocol message. Passwords and traffic never enter SQLite.
type Message struct {
	Type    string          `json:"type"`
	Session string          `json:"session,omitempty"`
	From    string          `json:"from,omitempty"`
	To      string          `json:"to,omitempty"`
	Body    json.RawMessage `json:"body,omitempty"`
	Error   string          `json:"error,omitempty"`
}

type peer struct {
	id        string
	ownerID   uint
	socket    *websocket.Conn
	out       chan Message
	dataOut   chan Message
	dataIn    chan Message
	done      chan struct{}
	once      sync.Once
	ctx       context.Context
	cancel    context.CancelFunc
	budget    *rate.Limiter
	control   *rate.Limiter
	written   chan struct{}
	processed chan struct{}
}

func (p *peer) stop() {
	p.once.Do(func() {
		close(p.done)
		if p.cancel != nil {
			p.cancel()
		}
		if p.socket != nil {
			_ = p.socket.Close()
		}
	})
}
func (p *peer) send(m Message) bool {
	select {
	case <-p.done:
		return false
	default:
	}
	queue := p.out
	if m.Type == "data" {
		queue = p.dataOut
	}
	select {
	case queue <- m:
		return true
	default:
		p.stop()
		return false
	}
}

type session struct {
	origin, owner string
	accepted      bool
	stage         int
	expires       time.Time
	createdAt     time.Time
}
type Relay struct {
	db                 *gorm.DB
	mu                 sync.RWMutex
	peers              map[string]*peer
	sessions           map[string]*session
	pendingSessions    map[string]struct{}
	sessionOrigins     map[string]int
	sessionOwners      map[string]int
	deviceSessions     map[string]int
	sessionIDsByDevice map[string]map[string]struct{}
	closed             bool
	admissionBudget    *rate.Limiter
	stop               chan struct{}
	once               sync.Once
	workers            sync.WaitGroup
	policyMu           sync.RWMutex // Policy changes exclude new sessions; peers may authenticate concurrently.
	config             RelayConfig
	console            *Console
	startedAt          time.Time
	node               atomic.Pointer[NodeSettings]
	metrics            atomic.Pointer[ServerMetrics]
	receivedBytes      atomic.Uint64
	sentBytes          atomic.Uint64
}

func newRelay(path, admission string) (*Relay, error) {
	return newRelayWithConfig(RelayConfig{Listen: "127.0.0.1:7080", Database: path, AdmissionKey: admission, BandwidthBitsPerSecond: defaultBandwidthBitsPerSecond})
}

func newRelayWithConfig(config RelayConfig) (*Relay, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	db, err := openRelayDatabase(config)
	if err != nil {
		return nil, err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	if err = db.AutoMigrate(&Device{}); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	r := &Relay{
		db: db, peers: make(map[string]*peer), sessions: make(map[string]*session),
		pendingSessions: make(map[string]struct{}),
		sessionOrigins:  make(map[string]int), sessionOwners: make(map[string]int),
		deviceSessions: make(map[string]int), sessionIDsByDevice: make(map[string]map[string]struct{}),
		admissionBudget: rate.NewLimiter(20, 40), stop: make(chan struct{}), config: config, startedAt: time.Now(),
	}
	settings := initialNodeSettings(config)
	r.node.Store(&settings)
	if config.ConsoleEnabled {
		r.console, err = newConsole(r)
		if err != nil {
			_ = sqlDB.Close()
			return nil, err
		}
		r.config.BootstrapPassword = ""
	}
	telemetry := &telemetrySampler{}
	if r.console != nil {
		r.sampleTelemetry(telemetry)
	}
	r.workers.Add(1)
	go func() {
		defer r.workers.Done()
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-r.stop:
				return
			case <-ticker.C:
				if r.console != nil {
					r.sampleTelemetry(telemetry)
				}
				r.mu.Lock()
				r.expirePendingSessionsLocked(time.Now(), true)
				r.mu.Unlock()
			}
		}
	}()
	return r, nil
}

func (r *Relay) router() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	g := gin.New() // No request/body/token logging.
	_ = g.SetTrustedProxies(nil)
	g.Use(gin.Recovery(), func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMessage)
		if strings.HasPrefix(c.Request.URL.Path, "/v1/") && !r.admissionBudget.Allow() {
			c.AbortWithStatusJSON(429, gin.H{"error": "admission rate limit"})
			return
		}
		c.Next()
	})
	g.GET("/health", func(c *gin.Context) { c.JSON(200, gin.H{"status": "ok", "protocol": 1}) })
	g.POST("/v1/register", r.register)
	g.GET("/v1/devices/:id", r.lookup)
	g.GET("/v1/channel", r.channel)
	g.GET("/ready", r.readiness)
	if r.console != nil {
		r.console.routes(g)
	}
	return g
}
func hashCredential(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
func bearer(c *gin.Context) string {
	return strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")
}
func (r *Relay) authenticate(c *gin.Context) (string, bool) {
	value := bearer(c)
	if len(value) != 44 {
		c.JSON(401, gin.H{"error": "device authentication failed"})
		return "", false
	}
	var device Device
	var bits int64
	if r.console != nil {
		var row struct {
			ID             string `gorm:"column:id"`
			OwnerID        *uint  `gorm:"column:owner_id"`
			Disabled       bool   `gorm:"column:disabled"`
			OwnerDisabled  *bool  `gorm:"column:owner_disabled"`
			OwnerBandwidth *int64 `gorm:"column:owner_bandwidth_bits_per_second"`
		}
		err := r.db.Table("devices AS d").
			Select("d.id, d.owner_id, d.disabled, u.disabled AS owner_disabled, u.bandwidth_bits_per_second AS owner_bandwidth_bits_per_second").
			Joins("LEFT JOIN users AS u ON u.id = d.owner_id").
			Where("d.id = ? AND d.credential_hash = ?", c.GetHeader("X-Device-ID"), hashCredential(value)).
			Take(&row).Error
		if err != nil {
			c.JSON(401, gin.H{"error": "device authentication failed"})
			return "", false
		}
		device = Device{ID: row.ID, OwnerID: row.OwnerID, Disabled: row.Disabled}
		if device.Disabled || (device.OwnerID != nil && (row.OwnerDisabled == nil || *row.OwnerDisabled || row.OwnerBandwidth == nil)) {
			c.JSON(403, gin.H{"error": "device or owner is disabled"})
			return "", false
		}
		bits = r.node.Load().DefaultBandwidthBitsPerSecond
		if device.OwnerID != nil {
			bits = *row.OwnerBandwidth
		}
	} else {
		err := r.db.Select("id", "credential_hash", "owner_id", "disabled").
			Where("id = ? AND credential_hash = ?", c.GetHeader("X-Device-ID"), hashCredential(value)).
			First(&device).Error
		if err != nil {
			c.JSON(401, gin.H{"error": "device authentication failed"})
			return "", false
		}
		if device.Disabled || device.OwnerID != nil {
			c.JSON(403, gin.H{"error": "device or owner is disabled"})
			return "", false
		}
		bits = r.node.Load().DefaultBandwidthBitsPerSecond
	}
	if bits <= 0 {
		c.JSON(403, gin.H{"error": "device or owner is disabled"})
		return "", false
	}
	c.Set("device", device)
	c.Set("deviceBandwidthBitsPerSecond", bits)
	return device.ID, true
}
func (r *Relay) register(c *gin.Context) {
	r.policyMu.Lock()
	defer r.policyMu.Unlock()
	if !r.admissionAllowed(bearer(c)) {
		c.JSON(401, gin.H{"error": "admission denied"})
		return
	}
	var input struct {
		Credential string `json:"credential"`
	}
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(400, gin.H{"error": "invalid registration"})
		return
	}
	raw, err := base64.StdEncoding.DecodeString(input.Credential)
	if err != nil || len(raw) != 32 {
		c.JSON(400, gin.H{"error": "credential must contain 32 random bytes"})
		return
	}
	digest := hashCredential(input.Credential)
	// Stable pseudonymous ID; never depends on machine serial numbers or account names.
	device, bits, err := r.registerDevice(digest)
	if err != nil {
		c.JSON(403, gin.H{"error": "device registration denied or storage unavailable"})
		return
	}
	c.JSON(200, gin.H{"id": device.ID, "bitsPerSecond": bits, "protocol": 1})
}
func (r *Relay) lookup(c *gin.Context) {
	if _, ok := r.authenticate(c); !ok {
		return
	}
	r.mu.RLock()
	_, online := r.peers[c.Param("id")]
	r.mu.RUnlock()
	c.JSON(200, gin.H{"id": c.Param("id"), "online": online})
}

var upgrade = websocket.Upgrader{ReadBufferSize: 4096, WriteBufferSize: 4096, WriteBufferPool: &sync.Pool{}, HandshakeTimeout: 10 * time.Second,
	CheckOrigin: func(req *http.Request) bool { return req.Header.Get("Origin") == "" }}

func (r *Relay) channel(c *gin.Context) {
	r.policyMu.RLock()
	id, ok := r.authenticate(c)
	if !ok {
		r.policyMu.RUnlock()
		return
	}
	device := c.MustGet("device").(Device)
	bits := c.MustGet("deviceBandwidthBitsPerSecond").(int64)
	socket, err := upgrade.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		r.policyMu.RUnlock()
		return
	}
	bytesPerSecond := float64(bits) / 8
	ctx, cancel := context.WithCancel(context.Background())
	p := &peer{id: id, socket: socket, out: make(chan Message, 64), dataOut: make(chan Message, 64), dataIn: make(chan Message, 64), done: make(chan struct{}), ctx: ctx, cancel: cancel, written: make(chan struct{}), processed: make(chan struct{}), control: rate.NewLimiter(20, 40)}
	if device.OwnerID != nil {
		p.ownerID = *device.OwnerID
	}
	r.mu.Lock()
	if r.closed || len(r.peers) >= 1024 || r.peers[id] != nil {
		r.mu.Unlock()
		r.policyMu.RUnlock()
		p.stop()
		return
	}
	// Account devices share one bidirectional budget. Anonymous peers each keep their own.
	// Reuse only active peers: a policy update stops old peers before new admission.
	if p.ownerID != 0 {
		for _, existing := range r.peers {
			if existing.ownerID != p.ownerID {
				continue
			}
			select {
			case <-existing.done:
				continue
			default:
			}
			p.budget = existing.budget
			break
		}
	}
	if p.budget == nil {
		p.budget = rate.NewLimiter(rate.Limit(bytesPerSecond), 64*1024)
	}
	r.peers[id] = p
	r.workers.Add(1)
	r.mu.Unlock()
	r.policyMu.RUnlock()
	defer r.remove(p)
	defer r.workers.Done()
	socket.SetReadLimit(maxMessage)
	_ = socket.SetReadDeadline(time.Now().Add(websocketReadIdleTimeout))
	socket.SetPongHandler(func(string) error { return socket.SetReadDeadline(time.Now().Add(websocketReadIdleTimeout)) })
	socket.SetPingHandler(func(payload string) error {
		return socket.WriteControl(websocket.PongMessage, []byte(payload), time.Now().Add(websocketWriteIdleTimeout))
	})
	go r.writeLoop(p)
	go r.processData(p)
	defer func() { p.stop(); <-p.written; <-p.processed }()
	p.send(Message{Type: "online", From: id})
	for {
		var m Message
		if err = readJSONWithProgress(socket, &m); err != nil {
			return
		}
		_ = socket.SetReadDeadline(time.Now().Add(websocketReadIdleTimeout))
		if m.Type != "data" && !p.control.Allow() {
			return
		}
		if m.Type == "heartbeat" {
			p.send(Message{Type: "heartbeat"})
			continue
		}
		if m.Type == "data" {
			if len(m.Body) > 64*1024 {
				return
			}
			// Keep reading Ping/Pong and heartbeats while data waits for bandwidth.
			select {
			case p.dataIn <- m:
			default:
				return
			}
			continue
		}
		r.route(p, m)
	}
}

type progressReader struct {
	socket *websocket.Conn
	reader io.Reader
}

func (r progressReader) Read(data []byte) (int, error) {
	n, err := r.reader.Read(data)
	if n > 0 {
		_ = r.socket.SetReadDeadline(time.Now().Add(websocketReadIdleTimeout))
	}
	return n, err
}

// readJSONWithProgress preserves the existing single JSON text message while
// extending the read deadline whenever a slow peer delivers more bytes. The
// WebSocket read limit still bounds the complete message.
func readJSONWithProgress(socket *websocket.Conn, value any) error {
	_, reader, err := socket.NextReader()
	if err != nil {
		return err
	}
	return json.NewDecoder(progressReader{socket: socket, reader: reader}).Decode(value)
}

func (r *Relay) processData(p *peer) {
	defer close(p.processed)
	for {
		select {
		case <-p.done:
			return
		case m := <-p.dataIn:
			if p.budget.WaitN(p.ctx, len(m.Body)) != nil {
				return
			}
			r.route(p, m)
		}
	}
}
func (r *Relay) writeLoop(p *peer) {
	defer close(p.written)
	ticker := time.NewTicker(websocketPingInterval)
	defer ticker.Stop()
	defer p.stop()
	timer := time.NewTimer(time.Hour)
	if !timer.Stop() {
		<-timer.C
	}
	defer timer.Stop()
	write := func(m Message) bool {
		if writeJSONWithProgress(p.socket, m) != nil {
			return false
		}
		if m.Type == "data" {
			r.sentBytes.Add(uint64(len(m.Body)))
		}
		return true
	}
	ping := func() bool {
		return p.socket.WriteControl(websocket.PingMessage, nil, time.Now().Add(10*time.Second)) == nil
	}
	for {
		// Control is independent of data reservations and cannot starve behind a slow quota.
		select {
		case m := <-p.out:
			if !write(m) {
				return
			}
			continue
		default:
		}
		select {
		case <-p.done:
			return
		case <-ticker.C:
			if !ping() {
				return
			}
		case m := <-p.out:
			if !write(m) {
				return
			}
		case m := <-p.dataOut:
			reservation := p.budget.ReserveN(time.Now(), len(m.Body))
			if !reservation.OK() {
				return
			}
			delay := reservation.Delay()
			if delay <= 0 {
				if !write(m) {
					return
				}
				continue
			}
			timer.Reset(delay)
			ready := false
			for !ready {
				select {
				case <-p.done:
					timer.Stop()
					reservation.Cancel()
					return
				case <-timer.C:
					ready = true
				case <-ticker.C:
					if !ping() {
						timer.Stop()
						reservation.Cancel()
						return
					}
				case control := <-p.out:
					if !write(control) {
						timer.Stop()
						reservation.Cancel()
						return
					}
				}
			}
			if !write(m) {
				return
			}
		}
	}
}

// writeJSONWithProgress keeps the wire-level JSON message unchanged while
// allowing the WebSocket transport to fragment large messages. Nginx and
// other reverse proxies may apply a per-connection response limit; a single
// WriteJSON call would apply one deadline to the entire message and could
// close an otherwise healthy connection before a throttled link finishes it.
// Each fragment gets a fresh idle deadline, while the caller's bounded queue
// still provides backpressure for a receiver that stops consuming data.
func writeJSONWithProgress(socket *websocket.Conn, message Message) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return err
	}
	// WriteJSON uses Encoder.Encode, which terminates every JSON message with a
	// newline. Preserve that exact framing detail for existing peers.
	payload = append(payload, '\n')
	writer, err := socket.NextWriter(websocket.TextMessage)
	if err != nil {
		return err
	}
	for len(payload) > 0 {
		n := websocketWriteChunkSize
		if len(payload) < n {
			n = len(payload)
		}
		_ = socket.SetWriteDeadline(time.Now().Add(websocketWriteIdleTimeout))
		if _, err := writer.Write(payload[:n]); err != nil {
			_ = writer.Close()
			return err
		}
		payload = payload[n:]
	}
	_ = socket.SetWriteDeadline(time.Now().Add(websocketWriteIdleTimeout))
	return writer.Close()
}
func (r *Relay) route(p *peer, m Message) {
	if m.Type == "data" {
		r.routeData(p, m)
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	fail := func(reason string) {
		response := Message{Type: "error", Session: m.Session, Error: reason}
		if m.Type == "connect" {
			var hello struct {
				RequestID string `json:"requestId"`
			}
			if json.Unmarshal(m.Body, &hello) == nil && len(hello.RequestID) == 32 {
				response.Body, _ = json.Marshal(hello)
			}
		}
		p.send(response)
	}
	if m.Type == "connect" {
		now := time.Now()
		r.expireDeviceSessionsLocked(p.id, now)
		r.expireDeviceSessionsLocked(m.To, now)
		target := r.peers[m.To]
		if target == nil || m.To == p.id {
			fail("target offline or invalid")
			return
		}
		if len(r.sessions) >= 4096 {
			fail("session capacity reached")
			return
		}
		if r.sessionOwners[p.id] > 0 {
			fail("当前作为被连接方，不能发起连接；请先结束入站连接或待授权请求。")
			return
		}
		if r.sessionOrigins[m.To] > 0 {
			fail("对方当前作为连接发起方，暂不接受入站连接。")
			return
		}
		if r.deviceSessions[p.id] >= 16 || r.deviceSessions[m.To] >= 16 {
			fail("device session capacity reached")
			return
		}
		sid := make([]byte, 16)
		if _, err := rand.Read(sid); err != nil {
			fail("random source unavailable")
			return
		}
		m.Session = hex.EncodeToString(sid)
		r.addSessionLocked(m.Session, &session{origin: p.id, owner: m.To, expires: time.Now().Add(90 * time.Second), createdAt: time.Now().UTC()})
		m.From = p.id
		if !target.send(m) {
			r.removeSessionLocked(m.Session)
			fail("target unavailable")
		}
		p.send(Message{Type: "pending", Session: m.Session, Body: m.Body})
		return
	}
	s := r.sessions[m.Session]
	if s != nil && !s.accepted && time.Now().After(s.expires) {
		r.removeSessionLocked(m.Session)
		s = nil
	}
	if s == nil && (m.Type == "close" || m.Type == "data") {
		return
	} // Close is idempotent; queued data for an ended session must not create a spurious error.
	if s == nil || (s.origin != p.id && s.owner != p.id) {
		fail("session unavailable")
		return
	}
	targetID := s.owner
	if p.id == s.owner {
		targetID = s.origin
	}
	target := r.peers[targetID]
	if target == nil {
		r.removeSessionLocked(m.Session)
		fail("peer disconnected")
		return
	}
	switch m.Type {
	case "challenge", "accept":
		if p.id != s.owner {
			fail("invalid session role")
			return
		}
		if m.Type == "challenge" {
			if s.stage != 0 {
				fail("invalid handshake order")
				return
			}
			s.stage = 1
		}
		if m.Type == "accept" {
			if s.stage != 2 {
				fail("invalid handshake order")
				return
			}
			s.accepted = true
			s.stage = 3
			delete(r.pendingSessions, m.Session)
		}
	case "proof":
		if p.id != s.origin {
			fail("invalid session role")
			return
		}
		if s.stage != 1 {
			fail("invalid handshake order")
			return
		}
		s.stage = 2
	case "data", "relay":
		if !s.accepted {
			fail("session not authorized")
			return
		}
	case "close":
		r.removeSessionLocked(m.Session)
	default:
		fail("unsupported message")
		return
	}
	if !s.accepted && time.Now().After(s.expires) {
		r.removeSessionLocked(m.Session)
		fail("authorization expired")
		return
	}
	m.From = p.id
	m.To = targetID
	if target.send(m) && m.Type == "data" {
		r.receivedBytes.Add(uint64(len(m.Body)))
	}
}

// routeData is the high-volume path. Session lookup and forwarding only read shared
// routing state, so concurrent streams do not serialize behind control messages.
func (r *Relay) routeData(p *peer, m Message) {
	r.mu.RLock()
	s := r.sessions[m.Session]
	if s == nil {
		r.mu.RUnlock()
		return
	}
	fail := func(reason string) {
		p.send(Message{Type: "error", Session: m.Session, Error: reason})
	}
	if s.origin != p.id && s.owner != p.id {
		r.mu.RUnlock()
		fail("session unavailable")
		return
	}
	if !s.accepted {
		expired := time.Now().After(s.expires)
		r.mu.RUnlock()
		if expired {
			r.mu.Lock()
			if r.sessions[m.Session] == s {
				r.removeSessionLocked(m.Session)
			}
			r.mu.Unlock()
			return
		}
		fail("session not authorized")
		return
	}
	targetID := s.owner
	if p.id == s.owner {
		targetID = s.origin
	}
	target := r.peers[targetID]
	if target == nil {
		r.mu.RUnlock()
		r.mu.Lock()
		if r.sessions[m.Session] == s {
			r.removeSessionLocked(m.Session)
		}
		r.mu.Unlock()
		fail("peer disconnected")
		return
	}
	m.From = p.id
	m.To = targetID
	if target.send(m) && m.Type == "data" {
		r.receivedBytes.Add(uint64(len(m.Body)))
	}
	r.mu.RUnlock()
}

func (r *Relay) addSessionLocked(id string, s *session) {
	r.sessions[id] = s
	r.pendingSessions[id] = struct{}{}
	for _, device := range []string{s.origin, s.owner} {
		ids := r.sessionIDsByDevice[device]
		if ids == nil {
			ids = make(map[string]struct{})
			r.sessionIDsByDevice[device] = ids
		}
		ids[id] = struct{}{}
		r.deviceSessions[device]++
	}
	r.sessionOrigins[s.origin]++
	r.sessionOwners[s.owner]++
}

func (r *Relay) removeSessionLocked(id string) *session {
	s := r.sessions[id]
	if s == nil {
		return nil
	}
	delete(r.sessions, id)
	delete(r.pendingSessions, id)
	if r.sessionOrigins[s.origin] <= 1 {
		delete(r.sessionOrigins, s.origin)
	} else {
		r.sessionOrigins[s.origin]--
	}
	if r.sessionOwners[s.owner] <= 1 {
		delete(r.sessionOwners, s.owner)
	} else {
		r.sessionOwners[s.owner]--
	}
	for _, device := range []string{s.origin, s.owner} {
		if r.deviceSessions[device] <= 1 {
			delete(r.deviceSessions, device)
		} else {
			r.deviceSessions[device]--
		}
		if ids := r.sessionIDsByDevice[device]; ids != nil {
			delete(ids, id)
			if len(ids) == 0 {
				delete(r.sessionIDsByDevice, device)
			}
		}
	}
	return s
}

func (r *Relay) expirePendingSessionsLocked(now time.Time, notify bool) {
	for id := range r.pendingSessions {
		s := r.sessions[id]
		if s == nil || now.Before(s.expires) {
			continue
		}
		r.removeSessionLocked(id)
		if notify {
			for _, device := range []string{s.owner, s.origin} {
				if p := r.peers[device]; p != nil {
					p.send(Message{Type: "close", Session: id, Error: "authorization expired"})
				}
			}
		}
	}
}

func (r *Relay) expireDeviceSessionsLocked(device string, now time.Time) {
	for id := range r.sessionIDsByDevice[device] {
		s := r.sessions[id]
		if s == nil || s.accepted || now.Before(s.expires) {
			continue
		}
		r.removeSessionLocked(id)
		for _, participant := range []string{s.owner, s.origin} {
			if p := r.peers[participant]; p != nil {
				p.send(Message{Type: "close", Session: id, Error: "authorization expired"})
			}
		}
	}
}

func (r *Relay) remove(p *peer) {
	p.stop()
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.peers[p.id] != p {
		return
	}
	delete(r.peers, p.id)
	for id := range r.sessionIDsByDevice[p.id] {
		s := r.removeSessionLocked(id)
		if s != nil {
			other := s.owner
			if other == p.id {
				other = s.origin
			}
			if q := r.peers[other]; q != nil {
				q.send(Message{Type: "close", Session: id, Error: "peer disconnected"})
			}
		}
	}
}
func (r *Relay) close() error {
	var err error
	r.once.Do(func() {
		r.mu.Lock()
		r.closed = true
		close(r.stop)
		for _, p := range r.peers {
			p.stop()
		}
		r.mu.Unlock()
		r.workers.Wait()
		if r.console != nil {
			r.console.close()
		}
		db, e := r.db.DB()
		if e != nil {
			err = e
		} else {
			err = db.Close()
		}
	})
	return err
}
