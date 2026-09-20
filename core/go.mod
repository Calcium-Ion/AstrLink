module github.com/QuantumNous/astrlink/core

go 1.25.1

require (
	github.com/QuantumNous/astrlink/convo v0.0.0
	github.com/QuantumNous/new-api/relaykit v0.2.0
	github.com/expr-lang/expr v1.17.6
	github.com/zalando/go-keyring v0.2.6
	golang.org/x/net v0.56.0
	golang.org/x/sys v0.46.0
	modernc.org/sqlite v1.38.2
)

// convo is developed in this repository; consumers outside the monorepo fetch
// it through the nested-module tag convo/vX.Y.Z instead.
replace github.com/QuantumNous/astrlink/convo => ../convo

require (
	al.essio.dev/pkg/shellescape v1.5.1 // indirect
	github.com/danieljoos/wincred v1.2.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/godbus/dbus/v5 v5.1.0 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v0.1.9 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/samber/lo v1.53.0 // indirect
	github.com/tidwall/gjson v1.19.0 // indirect
	github.com/tidwall/match v1.1.1 // indirect
	github.com/tidwall/pretty v1.2.0 // indirect
	github.com/tidwall/sjson v1.2.5 // indirect
	golang.org/x/exp v0.0.0-20250620022241-b7579e27df2b // indirect
	golang.org/x/text v0.38.0 // indirect
	modernc.org/libc v1.66.3 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
