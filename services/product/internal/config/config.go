package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type Config struct {
	App        App
	Log        Log
	PG         PG
	GRPCServer GRPCServer
}

func (c Config) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.App),
		validation.Field(&c.Log),
		validation.Field(&c.PG),
		validation.Field(&c.GRPCServer),
	)
}

type App struct {
	Name string `env:"APP_NAME,required"`
}

func (a App) Validate() error {
	return validation.ValidateStruct(&a,
		validation.Field(&a.Name, validation.Required),
	)
}

type Log struct {
	Level  string `env:"LOG_LEVEL,required"`
	Format string `env:"LOG_FORMAT,required"`
}

func (l Log) Validate() error {
	return validation.ValidateStruct(&l,
		validation.Field(&l.Level, validation.Required, validation.In("debug", "info", "warn", "error")),
		validation.Field(&l.Format, validation.Required, validation.In("text", "json")),
	)
}

type PG struct {
	DSN      string `env:"PG_DSN,required"`
	MaxConns int32  `env:"PG_MAX_CONNS,required"`
}

func (p PG) Validate() error {
	return validation.ValidateStruct(&p,
		validation.Field(&p.DSN, validation.Required),
		validation.Field(&p.MaxConns, validation.Required),
	)
}

type GRPCServer struct {
	Port       int  `env:"GRPC_SERVER_PORT,required"`
	Reflection bool `env:"GRPC_SERVER_REFLECTION,required"`
}

func (g GRPCServer) Validate() error {
	return validation.ValidateStruct(&g,
		validation.Field(&g.Port, validation.Required),
		validation.Field(&g.Reflection, validation.Required),
	)
}

func New() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("config: parse env: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("config: validate: %w", err)
	}

	return &cfg, nil
}
