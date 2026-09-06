module github.com/maksimegorovdev/delivery-backend/services/user

go 1.27.0

require (
	buf.build/go/protovalidate v1.4.0
	github.com/caarlos0/env/v11 v11.4.1
	github.com/go-ozzo/ozzo-validation/v4 v4.4.1
	github.com/jackc/pgx/v5 v5.10.0
	github.com/maksimegorovdev/delivery-backend/platform v0.0.0
	github.com/maksimegorovdev/delivery-backend/proto v0.0.0
	golang.org/x/sync v0.22.0
	google.golang.org/grpc v1.83.2
	google.golang.org/protobuf v1.36.12
)

require (
	buf.build/gen/go/bufbuild/protovalidate/protocolbuffers/go v1.36.12-20260825204119-511051f7f437.1 // indirect
	cel.dev/cel-go v0.32.0 // indirect
	cel.dev/expr v0.25.3 // indirect
	github.com/antlr4-go/antlr/v4 v4.13.1 // indirect
	github.com/jackc/pgerrcode v0.0.0-20250907135507-afb5586c32a6 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	go.yaml.in/yaml/v3 v3.0.5 // indirect
	golang.org/x/exp v0.0.0-20260820142414-ca536658362e // indirect
	golang.org/x/net v0.58.0 // indirect
	golang.org/x/sys v0.47.0 // indirect
	golang.org/x/text v0.41.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20260819154853-08b0e4226688 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20260825221802-da73d73af1c5 // indirect
)

replace github.com/maksimegorovdev/delivery-backend/platform => ../../platform

replace github.com/maksimegorovdev/delivery-backend/proto => ../../proto
