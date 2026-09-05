FROM golang:1.25 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /todo .

FROM gcr.io/distroless/static-debian12:nonroot
# Lambda Web Adapter: extension として同梱するだけで HTTP アプリがそのまま Lambda で動く
COPY --from=public.ecr.aws/awsguru/aws-lambda-adapter:0.9.1 /lambda-adapter /opt/extensions/lambda-adapter
COPY --from=build /todo /todo
ENV PORT=8080
ENV AWS_LWA_READINESS_CHECK_PATH=/healthz
EXPOSE 8080
ENTRYPOINT ["/todo"]
