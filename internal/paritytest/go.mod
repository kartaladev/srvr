// Module paritytest is the cross-engine parity suite. It is test-only and
// never released.
module github.com/kartaladev/srvr/internal/paritytest

go 1.27

replace (
	github.com/kartaladev/srvr => ../../
	github.com/kartaladev/srvr/fibersrvr => ../../fibersrvr
	github.com/kartaladev/srvr/ginsrvr => ../../ginsrvr
	github.com/kartaladev/srvr/otelsrvr => ../../otelsrvr
)

require (
	github.com/gin-gonic/gin v1.12.0
	github.com/gofiber/fiber/v3 v3.7.0
	github.com/kartaladev/prblm v0.3.0
	github.com/kartaladev/srvr v0.2.0
	github.com/kartaladev/srvr/fibersrvr v0.0.0-00010101000000-000000000000
	github.com/kartaladev/srvr/ginsrvr v0.0.0-00010101000000-000000000000
	github.com/kartaladev/srvr/otelsrvr v0.0.0-00010101000000-000000000000
	go.opentelemetry.io/otel v1.47.0
	go.opentelemetry.io/otel/sdk v1.47.0
	go.opentelemetry.io/otel/sdk/metric v1.47.0
	go.uber.org/goleak v1.3.0
)

require (
	github.com/bytedance/gopkg v0.1.3 // indirect
	github.com/bytedance/sonic v1.15.0 // indirect
	github.com/bytedance/sonic/loader v0.5.0 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/cloudwego/base64x v0.1.6 // indirect
	github.com/gabriel-vasile/mimetype v1.4.12 // indirect
	github.com/gin-contrib/sse v1.1.0 // indirect
	github.com/go-logr/logr v1.4.4 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/go-playground/locales v0.14.1 // indirect
	github.com/go-playground/universal-translator v0.18.1 // indirect
	github.com/go-playground/validator/v10 v10.30.1 // indirect
	github.com/goccy/go-json v0.10.5 // indirect
	github.com/goccy/go-yaml v1.19.2 // indirect
	github.com/gofiber/schema v1.9.3 // indirect
	github.com/gofiber/utils/v2 v2.6.4 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/json-iterator/go v1.1.12 // indirect
	github.com/kartaladev/prblm/adapters/fiberprblm v0.3.0 // indirect
	github.com/kartaladev/prblm/adapters/ginprblm v0.3.0 // indirect
	github.com/klauspost/compress v1.20.3 // indirect
	github.com/klauspost/cpuid/v2 v2.3.0 // indirect
	github.com/leodido/go-urn v1.4.0 // indirect
	github.com/mattn/go-colorable v0.1.16 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/modern-go/concurrent v0.0.0-20180306012644-bacd9c7ef1dd // indirect
	github.com/modern-go/reflect2 v1.0.2 // indirect
	github.com/molecule-man/go-brrr v1.3.1 // indirect
	github.com/pelletier/go-toml/v2 v2.2.4 // indirect
	github.com/philhofer/fwd v1.2.1 // indirect
	github.com/quic-go/qpack v0.6.0 // indirect
	github.com/quic-go/quic-go v0.59.1 // indirect
	github.com/tinylib/msgp v1.6.5 // indirect
	github.com/twitchyliquid64/golang-asm v0.15.1 // indirect
	github.com/ugorji/go/codec v1.3.1 // indirect
	github.com/valyala/bytebufferpool v1.0.0 // indirect
	github.com/valyala/fasthttp v1.78.0 // indirect
	go.mongodb.org/mongo-driver/v2 v2.5.0 // indirect
	go.opentelemetry.io/auto/sdk v1.2.1 // indirect
	go.opentelemetry.io/otel/log v1.47.0 // indirect
	go.opentelemetry.io/otel/metric v1.47.0 // indirect
	go.opentelemetry.io/otel/trace v1.47.0 // indirect
	golang.org/x/arch v0.22.0 // indirect
	golang.org/x/crypto v0.59.0 // indirect
	golang.org/x/net v0.63.0 // indirect
	golang.org/x/sys v0.50.0 // indirect
	golang.org/x/text v0.44.0 // indirect
	google.golang.org/protobuf v1.36.10 // indirect
)
