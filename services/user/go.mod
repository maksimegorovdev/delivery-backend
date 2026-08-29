module github.com/maksimegorovdev/delivery-backend/services/user

go 1.27.0

require (
	github.com/caarlos0/env/v11 v11.4.1
	github.com/go-ozzo/ozzo-validation/v4 v4.4.1
	github.com/jackc/pgx/v5 v5.10.0
	github.com/maksimegorovdev/delivery-backend/platform v0.0.0
)

require (
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	golang.org/x/sync v0.22.0 // indirect
	golang.org/x/text v0.41.0 // indirect
)

replace github.com/maksimegorovdev/delivery-backend/platform => ../../platform
