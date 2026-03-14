module maven-pkguard

go 1.26

require PKGuard/common v0.0.0

require (
	github.com/kr/pretty v0.2.1 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace PKGuard/common => ../common
