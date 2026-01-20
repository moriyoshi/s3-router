package config

import (
	"time"

	"github.com/moriyoshi/s3-router/internal/config/ir"
)

// AuthConfig holds authentication-related configuration.
type AuthConfig struct {
	DefaultRegion   string
	ClockSkewLeeway time.Duration
}

// populateAuthConfigFromIR populates an AuthConfig from its intermediate representation.
func buildAuthConfigFromIR(ctx *Context, src *ir.AuthConfig) *AuthConfig {
	dst := new(AuthConfig)
	var err error
	dst.DefaultRegion = src.DefaultRegion
	clockSkewLeeway := time.Second * 600
	if src.ClockSkewLeeway != "" {
		clockSkewLeeway, err = parseDuration(src.ClockSkewLeeway)
		if err != nil {
			ctx.Enter("ClockSkewLeeway").Append(err)
		}
	}
	dst.ClockSkewLeeway = clockSkewLeeway
	return dst
}
