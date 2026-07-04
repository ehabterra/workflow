module github.com/ehabterra/workflow/examples/timer_escalation_beanstalkd

go 1.25.0

require (
	github.com/beanstalkd/go-beanstalk v0.2.0
	github.com/ehabterra/workflow v0.0.0
	github.com/mattn/go-sqlite3 v1.14.22
)

require (
	github.com/expr-lang/expr v1.17.7 // indirect
	gopkg.in/yaml.v3 v3.0.1 // indirect
)

replace github.com/ehabterra/workflow => ../../
