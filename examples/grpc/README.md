# gRPC 示例

这个示例演示如何在**不写 `.proto`、不跑 protoc** 的前提下使用 `sprout/rpc`：
消息是带 `pb` 标签的普通 Go 结构体，框架在运行时把它们反射成真正的 protobuf
描述符，线格式与 protoc 产物完全一致。

服务端和客户端是两个独立的进程，因此这个示例也是一次真实的跨进程验证。

## 目录结构

```
examples/grpc/
├── messages/    两端共享的消息定义
├── server/      gRPC 服务端：一元方法 + 双向流式方法
└── client/      gRPC 客户端：调用一元方法 + 双向流式方法
```

## 运行

开两个终端。下面的命令都在 **sprout 仓库根目录** 下执行。

```bash
# 终端 1：启动服务端
go run ./examples/grpc/server

# 终端 2：调用服务端
go run ./examples/grpc/client
```

客户端的输出：

```
unary -> hello wxy (30)
stream -> echo hello
stream -> echo grpc
the stream is closed
```

## 环境变量

| 变量 | 适用 | 默认值 | 说明 |
|---|---|---|---|
| `GRPC_PORT` | 服务端 | `9090` | 监听端口 |
| `GRPC_ADDR` | 客户端 | `127.0.0.1:9090` | 服务端地址 |

## 要点

- 消息定义在 `messages` 包里，服务端与客户端共用同一份代码；把 `pb` 标签的
  字段号对齐，就能与 protoc 生成的其它语言客户端互操作。
- 服务端通过 `sprout.App.Runners` 随应用启动与停止，收到 `SIGINT`/`SIGTERM`
  时优雅退出。
- 客户端用 `sproutrpc.Doer` 与 `sproutrpc.StreamDoer` 拿到类型安全的调用闭包，
  与 `sprout/cli.Doer` 同风格。
- 客户端还可以用 `sproutrpc.WithNacos(...)` 经 Nacos 发现服务提供者，把
  `WithDirect` 换掉即可，其余代码不用改。
