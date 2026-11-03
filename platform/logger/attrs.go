package logger

import (
	"log/slog"
	"time"
)

func Err(err error) slog.Attr {
	return slog.Any("error", err)
}

// Duration возвращает атрибут duration_ms: длительность в миллисекундах дробным числом
// (592.334µs → 0.592334).
//
// [slog.Duration] здесь не подходит: JSONHandler и мост otelslog пишут её целыми наносекундами,
// и в ClickHouse под ключом duration_ms оказывалось 592334 вместо 0.59.
func Duration(d time.Duration) slog.Attr {
	return slog.Float64("duration_ms", float64(d)/float64(time.Millisecond))
}
