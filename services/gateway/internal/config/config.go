package config

import (
	"fmt"

	"github.com/caarlos0/env/v11"
	validation "github.com/go-ozzo/ozzo-validation/v4"
)

type Config struct {
	App          App
	Log          Log
	HTTP         HTTP
	OrderService OrderService
	FakeAuth     FakeAuth
}

func (c Config) Validate() error {
	return validation.ValidateStruct(&c,
		validation.Field(&c.App, validation.Required),
		validation.Field(&c.Log, validation.Required),
		validation.Field(&c.HTTP, validation.Required),
		validation.Field(&c.FakeAuth, validation.Required),
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

type HTTP struct {
	Addr string `env:"HTTP_ADDR,required"`
}

func (h HTTP) Validate() error {
	return validation.ValidateStruct(&h,
		validation.Field(&h.Addr, validation.Required),
	)
}

type OrderService struct {
	Addr string `env:"ORDER_SERVICE_GRPC_ADDR,required"`
}

func (o OrderService) Validate() error {
	return validation.ValidateStruct(&o,
		validation.Field(&o.Addr, validation.Required),
	)
}

type FakeAuth struct {
	UserID string `env:"FAKE_AUTH_USER_ID,required"`
}

func (f FakeAuth) Validate() error {
	return validation.ValidateStruct(&f,
		validation.Field(&f.UserID, validation.Required),
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
