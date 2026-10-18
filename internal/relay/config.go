// SPDX-License-Identifier: GPL-3.0-only
package relay

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"
)

type RelayConfig struct {
	Listen                 string `json:"listen"`
	Database               string `json:"database"`
	AdmissionKey           string `json:"admissionKey"`
	BandwidthBitsPerSecond int64  `json:"bandwidthBitsPerSecond"`
	DatabaseDriver         string `json:"databaseDriver"`
	MySQLDSN               string `json:"mysqlDsn"`
	RedisURL               string `json:"redisUrl"`
	RedisPrefix            string `json:"redisPrefix"`
	ConsoleEnabled         bool   `json:"consoleEnabled"`
	RegistrationEnabled    bool   `json:"registrationEnabled"`
	CookieSecure           bool   `json:"cookieSecure"`
	PublicURL              string `json:"publicUrl"`
	RelayName              string `json:"relayName"`
	BootstrapUsername      string `json:"-"`
	BootstrapPassword      string `json:"-"`
}

func defaultRelayConfig() RelayConfig {
	return RelayConfig{Listen: "127.0.0.1:7080", Database: "data/relay.sqlite", BandwidthBitsPerSecond: defaultBandwidthBitsPerSecond,
		DatabaseDriver: "sqlite", CookieSecure: true, RedisPrefix: "embeddesk:", RelayName: "EmbedDesk 中继"}
}

func (c RelayConfig) validate() error {
	if strings.TrimSpace(c.Listen) == "" || (c.driver() == "sqlite" && strings.TrimSpace(c.Database) == "") {
		return errors.New("listen and database are required")
	}
	_, port, err := net.SplitHostPort(c.Listen)
	if err != nil {
		return errors.New("listen must be HOST:PORT")
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("listen port must be between 1 and 65535")
	}
	if c.BandwidthBitsPerSecond < minBandwidthBitsPerSecond || c.BandwidthBitsPerSecond > maxBandwidthBitsPerSecond {
		return fmt.Errorf("bandwidthBitsPerSecond must be between %d and %d", minBandwidthBitsPerSecond, maxBandwidthBitsPerSecond)
	}
	return c.validateConsole()
}

func loadRelayConfig() (RelayConfig, error) {
	config := defaultRelayConfig()
	if path := os.Getenv("EMBEDDESK_CONFIG"); path != "" {
		file, err := os.Open(path)
		if err != nil {
			return RelayConfig{}, err
		}
		defer file.Close()
		data, err := io.ReadAll(io.LimitReader(file, 64*1024+1))
		if err != nil {
			return RelayConfig{}, err
		}
		if len(data) > 64*1024 {
			return RelayConfig{}, errors.New("configuration exceeds 64 KiB")
		}
		if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
			return RelayConfig{}, errors.New("configuration must be a JSON object")
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&config); err != nil {
			return RelayConfig{}, err
		}
		if decoder.Decode(new(any)) != io.EOF {
			return RelayConfig{}, errors.New("configuration must contain one JSON object")
		}
	}
	// Environment overrides remain useful for service managers and local smoke tests.
	if value := os.Getenv("EMBEDDESK_LISTEN"); value != "" {
		config.Listen = value
	}
	if value := os.Getenv("EMBEDDESK_DATABASE"); value != "" {
		config.Database = value
	}
	if value := os.Getenv("EMBEDDESK_ADMISSION_KEY"); value != "" {
		config.AdmissionKey = value
	}
	if value := os.Getenv("EMBEDDESK_BANDWIDTH_BITS_PER_SECOND"); value != "" {
		parsed, err := strconv.ParseInt(value, 10, 64)
		if err != nil {
			return RelayConfig{}, fmt.Errorf("invalid EMBEDDESK_BANDWIDTH_BITS_PER_SECOND: %w", err)
		}
		config.BandwidthBitsPerSecond = parsed
	}
	if err := config.loadConsoleEnv(); err != nil {
		return RelayConfig{}, err
	}
	return config, config.validate()
}
