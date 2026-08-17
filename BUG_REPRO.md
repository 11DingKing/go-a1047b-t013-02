# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

北极快航旺季的等待队列彻底不工作了，客户投诉说舱位满了之后就再也没人通知过他们。

复现很简单：这个航次 DG 舱位只有 1 个，第一家货代已经付了定金把它占住；危货堆存位给了 4 个，不是瓶颈。第二家货代来要舱位：

```
$ curl -sS -X POST localhost:58594/api/v1/bookings/BK-5/space
{"error":"voyage VY-1 has no battery capacity left: no capacity available"}
http_code=400

$ curl -s localhost:58594/api/v1/bookings/BK-5
"state":"storage_verified"

$ curl -s localhost:58594/api/v1/voyages/VY-1/capacity
{"voyage_id":"VY-1","available_dg":0,"available_gen":5,"holds_dg":0,"holds_gen":0,"waitlist_length":0,...}
```

该进等待队列的没进，waitlist_length 一直是 0，订舱卡在 storage_verified 不动。后面第一家取消退舱、舱位放出来了，也没有任何人被提上来——队列里本来就是空的。

还有一处对不上：同样一句「没有容量」，从危货堆存位那边返回来是 409：

```
$ curl -sS -X POST localhost:58594/api/v1/bookings/BK-8/storage -d '{"storage_location_id":"SL-6"}'
{"error":"no capacity available"}
http_code=409
```

而航次舱位这边是 400。错误文案里明明也有 no capacity available，状态码却落到了未归类那一档。普通货（非危货）走同一个接口是正常的——它那个舱位池还没满。

请先不要改代码。我需要你先把根因定位清楚：说明为什么舱位满了之后既不进等待队列、也不能被后续释放提升，为什么同一句「没有容量」在堆存位那边是 409 而在航次舱位这边变成 400，以及为什么错误文字看着完全正常。给出你实际执行过的复现命令、观察到的输出，以及支撑结论的代码位置。结论确认之后我们再谈怎么改。

## 含 Bug 版本

- 仓库：11DingKing/go-a1047b-t013-02
- 仓库地址：https://github.com/11DingKing/go-a1047b-t013-02.git
- parent SHA：bf45abae7236b8dde0de3f7b581556e025079e6d

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/go-a1047b-t013-02.git bug-repro
cd bug-repro
git checkout --detach bf45abae7236b8dde0de3f7b581556e025079e6d
go test ./internal/booking/ ./internal/httpapi/ -run "TestSaturatedCapacityWaitlistsInsteadOfFailing|TestWaitlistedBookingPromotedWhenSpaceFrees|TestMultipleWaitlistedBookingsQueueUp|TestGeneralCargoPoolIndependentOfDG|TestStorageCapacityStillReportsConflict|TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist|TestHTTPWaitlistedBookingPromotedAfterCancel|TestHTTPStorageFullStillReportsConflict" -count=1 -timeout=120s
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/booking/ ./internal/httpapi/ -run "TestSaturatedCapacityWaitlistsInsteadOfFailing|TestWaitlistedBookingPromotedWhenSpaceFrees|TestMultipleWaitlistedBookingsQueueUp|TestGeneralCargoPoolIndependentOfDG|TestStorageCapacityStillReportsConflict|TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist|TestHTTPWaitlistedBookingPromotedAfterCancel|TestHTTPStorageFullStillReportsConflict" -count=1 -timeout=120s
--- FAIL: TestSaturatedCapacityWaitlistsInsteadOfFailing (0.03s)
    waitlist_test.go:33: allocating space on a full voyage must not fail: voyage VY-1 has no battery capacity left: no capacity available
--- FAIL: TestWaitlistedBookingPromotedWhenSpaceFrees (0.00s)
    waitlist_test.go:93: allocate space: voyage VY-1 has no battery capacity left: no capacity available
--- FAIL: TestMultipleWaitlistedBookingsQueueUp (0.00s)
    waitlist_test.go:121: allocate space CNTR-B: voyage VY-1 has no battery capacity left: no capacity available
FAIL
FAIL	arcticexpress/internal/booking	0.122s
--- FAIL: TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist (0.05s)
    waitlist_test.go:109: allocate second: status 400, want 200; body map[error:voyage VY-1 has no battery capacity left: no capacity available]
--- FAIL: TestHTTPWaitlistedBookingPromotedAfterCancel (0.00s)
    waitlist_test.go:139: allocate second: status 400 body map[error:voyage VY-1 has no battery capacity left: no capacity available]
FAIL
FAIL	arcticexpress/internal/httpapi	0.101s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/booking/ ./internal/httpapi/ -run "TestSaturatedCapacityWaitlistsInsteadOfFailing|TestWaitlistedBookingPromotedWhenSpaceFrees|TestMultipleWaitlistedBookingsQueueUp|TestGeneralCargoPoolIndependentOfDG|TestStorageCapacityStillReportsConflict|TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist|TestHTTPWaitlistedBookingPromotedAfterCancel|TestHTTPStorageFullStillReportsConflict" -count=1 -timeout=120s
--- FAIL: TestSaturatedCapacityWaitlistsInsteadOfFailing (0.00s)
    waitlist_test.go:33: allocating space on a full voyage must not fail: voyage VY-1 has no battery capacity left: no capacity available
--- FAIL: TestWaitlistedBookingPromotedWhenSpaceFrees (0.00s)
    waitlist_test.go:93: allocate space: voyage VY-1 has no battery capacity left: no capacity available
--- FAIL: TestMultipleWaitlistedBookingsQueueUp (0.00s)
    waitlist_test.go:121: allocate space CNTR-B: voyage VY-1 has no battery capacity left: no capacity available
FAIL
FAIL	arcticexpress/internal/booking	0.004s
--- FAIL: TestHTTPSpaceRequestOnFullVoyageJoinsWaitlist (0.00s)
    waitlist_test.go:109: allocate second: status 400, want 200; body map[error:voyage VY-1 has no battery capacity left: no capacity available]
--- FAIL: TestHTTPWaitlistedBookingPromotedAfterCancel (0.00s)
    waitlist_test.go:139: allocate second: status 400 body map[error:voyage VY-1 has no battery capacity left: no capacity available]
FAIL
FAIL	arcticexpress/internal/httpapi	0.007s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

目标仓库零改动（git status 干净，无新增、修改或删除文件）。
准确指出出问题的 Go 文件与具体符号。
说明该符号的错误行为如何使上层按类别识别「没有容量」失效，从而导致：满舱订舱不进等待队列而直接报错、waitlist_length 恒为 0、后续释放运力时无人可提升，同时 HTTP 状态码从 409 退化为 400。
解释为何同一个哨兵错误经由危货堆存位路径返回时仍是 409，指出两条路径的差别所在。
给出实际执行过的复现命令与观察到的输出作为证据，而非仅凭阅读代码推断。
