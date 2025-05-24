build:
	go mod download
	go test -v ./...
	go build -v

generate-mocks:
	go generate ./...

test:
	go test -v ./...

test-coverage:
	go test ./... -cover

test-integration:
	go test ./pkg/runstream/ -integration

test-integration-coverage:
	go test ./pkg/runstream/ -integration -cover

docker:
	docker build -t tfbuddy:dev -f Dockerfile.server ./

minikube-up:
	minikube start --driver=docker
	minikube addons enable ingress
	minikube addons enable registry
	echo "Remember to run minikube tunnel on mac before running localdev"

tunnel:
	minikube tunnel

setup-localdev:
	tilt up

destroy-localdev:
	tilt down

.PHONY: up-local
up-local:
	tilt up

tail-local:
	stern --context minikube --namespace tfbuddy-localdev tfbuddy-v -s10s

generate-requirements-file:
	poetry export -f requirements.txt --output docs/requirements.txt --without-hashes