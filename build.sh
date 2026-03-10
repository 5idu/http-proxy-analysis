#! /bin/bash
cd ui
npm run build
cd ..
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -tags=jsoniter -ldflags "-s -w" -o ./bin/hpa ./main.go
docker build -t hellojqk/http-proxy-analysis:latest .
docker push hellojqk/http-proxy-analysis:latest

version=0.0.30
docker tag hellojqk/http-proxy-analysis:latest hellojqk/http-proxy-analysis:${version}
docker push hellojqk/http-proxy-analysis:${version}
