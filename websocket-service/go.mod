module novel-server/websocket-service

go 1.24.1

require (
	github.com/joho/godotenv v1.5.1 // Для .env файлов при локальной разработке
	github.com/kelseyhightower/envconfig v1.4.0
	github.com/labstack/echo/v4 v4.12.0
	github.com/prometheus/client_golang v1.19.1 // Prometheus клиент
	github.com/rabbitmq/amqp091-go v1.10.0
	github.com/rs/zerolog v1.33.0
)

require (
	github.com/gorilla/websocket v1.5.1
	github.com/labstack/gommon v0.4.2 // indirect
	github.com/mattn/go-colorable v0.1.13 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasttemplate v1.2.2 // indirect
	golang.org/x/crypto v0.31.0 // indirect
	golang.org/x/net v0.24.0 // indirect
	golang.org/x/sys v0.28.0 // indirect
	golang.org/x/text v0.21.0 // indirect
)

require novel-server/shared v0.0.0-00010101000000-000000000000

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cespare/xxhash/v2 v2.2.0 // indirect
	github.com/golang-jwt/jwt/v5 v5.2.2 // indirect
	github.com/prometheus/client_model v0.5.0 // indirect
	github.com/prometheus/common v0.48.0 // indirect
	github.com/prometheus/procfs v0.12.0 // indirect
	google.golang.org/protobuf v1.33.0 // indirect
)

// Заменяем shared на локальный путь для разработки
replace novel-server/shared => ../shared
