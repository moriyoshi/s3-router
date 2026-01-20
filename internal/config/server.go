package config

import (
	"time"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// ServerConfig holds HTTP server configuration parameters.
type ServerConfig struct {
	ReadTimeout    time.Duration
	WriteTimeout   time.Duration
	IdleTimeout    time.Duration
	MaxBodySize    int64
	RouteCacheSize int
}

// buildServerConfigFromIR constructs a ServerConfig from its intermediate representation.
func buildServerConfigFromIR(ctx *Context, src *ir.ServerConfig) *ServerConfig {
	if src == nil {
		return nil
	}

	parsedServer := new(ServerConfig)

	// Parse read timeout
	readTimeout, err := parseDuration(src.ReadTimeout)
	if err != nil {
		ctx.Enter("ReadTimeout").Append("invalid value: %w", err)
	}
	if readTimeout <= 0 {
		readTimeout = 15 * time.Second
	}
	parsedServer.ReadTimeout = readTimeout

	// Parse write timeout
	writeTimeout, err := parseDuration(src.WriteTimeout)
	if err != nil {
		ctx.Enter("WriteTimeout").Append("invalid value: %w", err)
	}
	if writeTimeout <= 0 {
		writeTimeout = 15 * time.Second
	}
	parsedServer.WriteTimeout = writeTimeout

	// Parse idle timeout
	idleTimeout, err := parseDuration(src.IdleTimeout)
	if err != nil {
		ctx.Enter("IdleTimeout").Append("invalid value: %w", err)
	}
	if idleTimeout <= 0 {
		idleTimeout = 60 * time.Second
	}
	parsedServer.IdleTimeout = idleTimeout

	// Parse max body size
	maxBodySize, err := parseBytes(src.MaxBodySize)
	if err != nil {
		ctx.Enter("MaxBodySize").Append("invalid value: %w", err)
	}
	if maxBodySize <= 0 {
		maxBodySize = 4 * 1024 * 1024 * 1024 // 4GB default
	}
	parsedServer.MaxBodySize = maxBodySize

	// Parse route cache size
	routeCacheSize, err := parseCount(src.RouteCacheSize)
	if err != nil {
		ctx.Enter("RouteCacheSize").Append("invalid value: %w", err)
	}
	if routeCacheSize == 0 {
		routeCacheSize = 1000
	}
	parsedServer.RouteCacheSize = routeCacheSize

	return parsedServer
}
