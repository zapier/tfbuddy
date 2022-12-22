build:
	go mod download
	go test -v ./...
	go build -v

generate-mocks:
	go generate ./...

test:
	go test -v ./...

docker:
	docker build -t tfbuddy:dev -f Dockerfile.server ./

minikube-up:
	minikube start --driver=hyperkit
	minikube addons enable ingress
	minikube addons enable registry
	echo "Remember to run minikube tunnel on mac before running localdev"

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