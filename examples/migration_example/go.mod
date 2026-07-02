module github.com/ehabterra/workflow/examples/migration_example

go 1.25.0

require (
	github.com/ehabterra/workflow v0.0.0
	github.com/golang-migrate/migrate/v4 v4.19.1
	github.com/mattn/go-sqlite3 v1.14.32
)

require github.com/expr-lang/expr v1.17.6 // indirect

replace github.com/ehabterra/workflow => ../../
