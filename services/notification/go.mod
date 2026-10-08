module github.com/maksimegorovdev/delivery-backend/services/notification

go 1.27.0

require (
	github.com/caarlos0/env/v11 v11.4.1
	github.com/go-ozzo/ozzo-validation/v4 v4.4.1
	github.com/jackc/pgx/v5 v5.10.0
	github.com/maksimegorovdev/delivery-backend/platform v0.0.0
	github.com/maksimegorovdev/delivery-backend/proto v0.0.0-20261004115919-6422b79f7e72
	golang.org/x/sync v0.23.0
	google.golang.org/protobuf v1.36.12
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.2 // indirect
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/klauspost/compress v1.20.0 // indirect
	github.com/pierrec/lz4/v4 v4.1.30 // indirect
	github.com/stretchr/testify v1.12.1 // indirect
	github.com/twmb/franz-go v1.22.1 // indirect
	github.com/twmb/franz-go/pkg/kmsg v1.14.0 // indirect
	golang.org/x/net v0.59.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
	golang.org/x/text v0.42.0 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
	google.golang.org/grpc v1.83.2 // indirect
)

replace github.com/maksimegorovdev/delivery-backend/platform => ../../platform
