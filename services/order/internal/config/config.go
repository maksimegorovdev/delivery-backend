package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	validation "github.com/go-ozzo/ozzo-validation/v4"
	"github.com/go-ozzo/ozzo-validation/v4/is"
)

type Config struct {
	App App
	Log Log
	PG  PG
}

func (c Config) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.App),
		validation.Field(&c.Log),
		validation.Field(&c.PG),
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
	URL string `env:"PG_URL,required"`
}

func (p PG) Validate() error {
	return validation.ValidateStruct(&p,
		validation.Field(&p.URL, validation.Required, is.URL),
	)
}

func New() (*Config, error) {
	var cfg Config
	if err := env.Parse(&cfg); err != nil {
		return nil, fmt.Errorf("parse: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate: %w", err)
	}

	return &cfg, nil
}
