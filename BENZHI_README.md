# BENZHI_README

## 项目说明

- 项目：11DingKing/go-a1047b-t013-02
- 项目用途：A Go backend for Haijie Shipping Arctic Express, coordinating Chuanshan-port container space and dangerous-goods storage for the 20-day direct Europe service. It serves cargo owners, freight forwarders, terminal operators, shipping companies and customs for booking the Ningbo/Yiwu → Rotterdam / Hamburg / Gdynia route.
- Go 工具链：`golang:1.26`
- 前端工具链：无

## 标准构建、运行和测试命令

进入容器后执行：

```bash
# 编译
cd '/app' && GOTOOLCHAIN=local go build ./...

# 启动
cd '/app' && GOTOOLCHAIN=local go run ./cmd/server

# 测试
cd '/app' && GOTOOLCHAIN=local go test ./...
```

## Docker 构建和进入容器

```bash
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-task-67-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-task-67-arm64 linux/arm64
docker run -it benzhi-task-67-amd64:latest
docker run -it --platform linux/arm64 benzhi-task-67-arm64:latest
```

## 题目验证命令

1. 预期退出码 1：`go test ./internal/booking/ ./internal/httpapi/ -run "TestSaturatedCapacityWaitlistsInsteadOfFailing|TestWaitlistedBookingPromotedWhenSpaceFrees|TestMultipleWaitlistedBookingsQueueUp|TestGeneralCargoPoolIndependentOfDG|TestStorageCapacityStillReportsConflict|TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist|TestHTTPWaitlistedBookingPromotedAfterCancel|TestHTTPStorageFullStillReportsConflict" -count=1 -timeout=120s`

## Bug 复现

Bug 现象、触发步骤和完整错误信息见 `BUG_REPRO.md`。
