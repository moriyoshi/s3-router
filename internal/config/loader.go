package config

import (
	"github.com/moriyoshi/s3-router/internal/config/ir"
)

func LoadFromFile(path string) (*Config, error) {
	var irConfig ir.Config
	err := irConfig.PopulateFromFile(path)
	if err != nil {
		return nil, err
	}
	cfg := new(Config)
	if err := cfg.PopulateFromIR(&irConfig); err != nil {
		return nil, err
	}
	return cfg, nil
}
