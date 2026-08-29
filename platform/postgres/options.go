package postgres

type Option func(*postgres)

func WithMaxConns(maxConns int32) Option {
	return func(p *postgres) {
		p.maxConns = maxConns
	}
}
