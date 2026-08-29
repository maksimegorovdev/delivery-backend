module github.com/maksimegorovdev/delivery-backend/services/notification

go 1.27.0

require (
	github.com/caarlos0/env/v11 v11.4.1
	github.com/go-ozzo/ozzo-validation/v4 v4.4.1
	github.com/maksimegorovdev/delivery-backend/platform v0.0.0
)

require github.com/asaskevich/govalidator v0.0.0-20210307081110-f21760c49a8d // indirect

replace github.com/maksimegorovdev/delivery-backend/platform => ../../platform
