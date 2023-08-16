VERSION 0.6

ARG token=""
ARG CI_REGISTRY_IMAGE="tfbuddy"
ARG CHART_RELEASER_VERSION="1.4.1"
ARG CHART_TESTING_VERSION="3.7.1"
ARG GOLANG_VERSION="1.19.3"
ARG HELM_VERSION="3.8.1"
ARG HELM_UNITTEST_VERSION="0.2.8"
ARG KUBECONFORM_VERSION="0.5.0"
ARG STATICCHECK_VERSION="0.3.3"

test:
    BUILD +ci-golang
    BUILD +ci-helm

ci-golang:
    # This should be enabled at some point
    # BUILD +lint-golang
    BUILD +validate-golang
    BUILD +test-golang

ci-helm:
    BUILD +test-helm

build:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +build-docker

release:
    BUILD --platform=linux/amd64 --platform=linux/arm64 +release-docker
    BUILD +release-helm

go-deps:
    FROM golang:${GOLANG_VERSION}-bullseye

    WORKDIR /src
    COPY go.mod go.sum /src
    RUN go mod download

build-binary:
    FROM +go-deps
    
    ARG GOOS=linux
    ARG GOARCH=amd64
    ARG VARIANT
    ARG --required GIT_TAG
    ARG --required GIT_COMMIT
    
    WORKDIR /src
    COPY . /src
    RUN GOARM=${VARIANT#v} go build -ldflags "-X github.com/zapier/tfbuddy/pkg.GitCommit=$GIT_COMMIT -X github.com/zapier/tfbuddy/pkg.GitTag=$GIT_TAG" -o tfbuddy  
    SAVE ARTIFACT tfbuddy

build-docker:
    FROM --platform=$TARGETPLATFORM ubuntu
    ARG TARGETPLATFORM
    ARG TARGETARCH
    ARG TARGETVARIANT

    ARG --required CI_REGISTRY_IMAGE
    ARG --required GIT_TAG
    ARG --required GIT_COMMIT

    COPY --platform=linux/amd64 \
         (+build-binary/tfbuddy --GOARCH=$TARGETARCH --VARIANT=$TARGETVARIANT) \
         /usr/bin/
    RUN mkdir /var/tfbuddy
    RUN /usr/bin/tfbuddy help
    RUN apt update && apt install -y ca-certificates
    CMD ["/usr/bin/tfbuddy", "tfc", "handler"]
    
    SAVE IMAGE --push ${CI_REGISTRY_IMAGE}:${GIT_COMMIT}

release-docker:
    FROM --platform=$TARGETPLATFORM ubuntu
    ARG TARGETPLATFORM
    ARG TARGETARCH
    ARG TARGETVARIANT

    ARG --required CI_REGISTRY_IMAGE
    ARG --required GIT_TAG
    ARG --required GIT_COMMIT

    COPY --platform=linux/amd64 \
         (+build-binary/tfbuddy --GOARCH=$TARGETARCH --VARIANT=$TARGETVARIANT) \
         /usr/bin/
    RUN mkdir /var/tfbuddy
    RUN /usr/bin/tfbuddy help
    RUN apt update && apt install -y ca-certificates
    CMD ["/usr/bin/tfbuddy", "tfc", "handler"]

    SAVE IMAGE --push ${CI_REGISTRY_IMAGE}:latest
    SAVE IMAGE --push ${CI_REGISTRY_IMAGE}:${GIT_COMMIT}
    SAVE IMAGE --push ${CI_REGISTRY_IMAGE}:${GIT_TAG}


lint-golang:
    FROM +go-deps

    # install staticcheck
    RUN FILE=staticcheck.tgz \
        && URL=https://github.com/dominikh/go-tools/releases/download/v${STATICCHECK_VERSION}/staticcheck_linux_amd64.tar.gz \
        && wget ${URL} \
            --output-document ${FILE} \
        && tar \
            --extract \
            --verbose \
            --directory /bin \
            --strip-components=1 \
            --file ${FILE} \
        && staticcheck -version

    WORKDIR /src
    COPY . /src
    RUN staticcheck ./...

validate-golang:
    FROM +go-deps

    WORKDIR /src
    COPY . /src
    RUN go vet ./...

test-golang:
    FROM +go-deps

    WORKDIR /src
    COPY . /src

    RUN go test -race ./...

test-helm:
    FROM quay.io/helmpack/chart-testing:v${CHART_TESTING_VERSION}

    # install kubeconform
    RUN FILE=kubeconform.tgz \
        && URL=https://github.com/yannh/kubeconform/releases/download/v${KUBECONFORM_VERSION}/kubeconform-linux-amd64.tar.gz \
        && wget ${URL} \
            --output-document ${FILE} \
        && tar \
            --extract \
            --verbose \
            --directory /bin \
            --file ${FILE} \
        && kubeconform -v

    RUN apk add --no-cache bash git \
        && helm plugin install --version "${HELM_UNITTEST_VERSION}" https://github.com/quintush/helm-unittest \
        && helm unittest --help \
        && helm repo add nats https://nats-io.github.io/k8s/helm/charts/
    # actually lint the chart
    WORKDIR /src
    COPY . /src
    RUN git fetch --prune --unshallow | true
    RUN ct --config ./.github/ct.yaml lint ./charts

release-helm:
    FROM quay.io/helmpack/chart-releaser:v${CHART_RELEASER_VERSION}

    RUN FILE=helm.tgz \
        && URL=https://get.helm.sh/helm-v${HELM_VERSION}-linux-amd64.tar.gz \
        && wget ${URL} \
            --output-document ${FILE} \
        && tar \
            --strip-components=1 \
            --extract \
            --verbose \
            --directory /bin \
            --file ${FILE} \
        && helm version

    WORKDIR /src
    COPY . /src
    RUN helm repo add nats https://nats-io.github.io/k8s/helm/charts/
    RUN cr --config .github/cr.yaml package charts/*
    SAVE ARTIFACT .cr-release-packages/ AS LOCAL ./dist

    RUN mkdir -p .cr-index
    RUN git config --global user.email "opensource@zapier.com"
    RUN git config --global user.name "Open Source at Zapier"
    RUN git fetch --prune --unshallow | true

    RUN --push cr --config .github/cr.yaml upload --token $token --skip-existing
    RUN --push cr --config .github/cr.yaml index --token $token --push