# syntax=docker/dockerfile:1
FROM --platform=linux/x86_64 golang:1.18 AS build
WORKDIR /build
COPY . .
ENV CGO_ENABLED=0 
ENV GOOS=linux

ARG VERSION
ARG COMMIT
ARG ANALYTICS_API_KEY
ARG ANALYTICS_TRACKING_ID
ARG SEGMENTIO_KEY
ARG SLACK_BOT_CLIENT_ID
ARG SLACK_BOT_CLIENT_SECRET
ARG TARGETOS TARGETARCH

RUN HP=$HTTP_PROXY
RUN HSP=$HTTPS_PROXY
ENV HTTP_PROXY http://127.0.0.1:3128/
ENV HTTPS_PROXY http://127.0.0.1:3128/

RUN cd cmd/api-server; \
    GOOS=$TARGETOS GOARCH=$TARGETARCH  go build \
        -ldflags "-X github.com/kubeshop/testkube/internal/pkg/api.Version=$VERSION \
            -X github.com/kubeshop/testkube/internal/pkg/api.Commit=$COMMIT \
			-X github.com/kubeshop/testkube/internal/app/api/v1.SlackBotClientID=$SLACK_BOT_CLIENT_ID \
			-X github.com/kubeshop/testkube/internal/app/api/v1.SlackBotClientSecret=$SLACK_BOT_CLIENT_SECRET \
            -X github.com/kubeshop/testkube/pkg/telemetry.TestkubeMeasurementID=$ANALYTICS_TRACKING_ID \
            -X github.com/kubeshop/testkube/pkg/telemetry.TestkubeMeasurementSecret=$ANALYTICS_API_KEY \
            -X github.com/kubeshop/testkube/pkg/telemetry.SegmentioKey=$SEGMENTIO_KEY" \
        -o /app -mod mod -a .

FROM alpine:3.16  
ENV HTTP_PROXY http://127.0.0.1:3128/
ENV HTTPS_PROXY http://127.0.0.1:3128/
RUN echo $HTTP_PROXY && echo $http_proxy && echo $HTTPS_PROXY && echo $https_proxy
RUN apk --no-cache add ca-certificates libssl1.1
WORKDIR /root/
COPY  --from=build /app /bin/app

ENV HTTP_PROXY $HP
ENV HTTPS_PROXY $HPS

USER 1001

EXPOSE 8088
CMD ["/bin/app"]
