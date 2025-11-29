module github.com/ehabterra/workflow/examples/migration_example

go 1.21

require (
	github.com/ehabterra/workflow v0.0.0
	github.com/golang-migrate/migrate/v4 v4.17.0
	github.com/mattn/go-sqlite3 v1.14.22
)

require (
	github.com/hashicorp/errwrap v1.1.0 // indirect
	github.com/hashicorp/go-multierror v1.1.1 // indirect
	go.uber.org/atomic v1.7.0 // indirect
)

replace github.com/ehabterra/workflow => ../../
