module github.com/Miosa-osa/miosa-cli-go

go 1.25.0

replace github.com/Miosa-osa/miosa-go => ./internal/miosa-sdk

require (
	github.com/BurntSushi/toml v1.6.0
	github.com/Miosa-osa/miosa-go v0.0.0-00010101000000-000000000000
	github.com/gorilla/websocket v1.5.3
	github.com/spf13/cobra v1.10.2
	github.com/spf13/pflag v1.0.9
	golang.org/x/term v0.42.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	golang.org/x/sys v0.43.0 // indirect
)
