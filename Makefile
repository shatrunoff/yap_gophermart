build:
	go mod tidy
	go mod vendor
	go build -o ./cmd/gophermart/server ./cmd/gophermart

accrual:
	./cmd/accrual/accrual_darwin_arm64 -a=localhost:8080

test:
	make build
	sh run_test.sh