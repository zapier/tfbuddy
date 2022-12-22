FROM ubuntu as server

RUN apt update && apt install -y ca-certificates

WORKDIR /app
COPY build/tfbuddy /app/tfbuddy

# sanity check build
RUN /app/tfbuddy help

CMD ["/app/tfbuddy", "tfc", "handler"]
