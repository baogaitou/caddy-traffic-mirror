# Caddy Traffic Mirror

[English](#english) | [中文](#中文)

---

## English

`caddy-traffic-mirror` is an HTTP handler module for Caddy v2 that mirrors incoming requests to a specified backend. The mirroring process is asynchronous and does not block or alter the original request-response flow.

This is useful for scenarios like traffic analysis, testing new service versions with production data, or passive data collection.

### Features

- **Asynchronous Mirroring**: Does not impact the latency of the original request.
- **Flexible Filtering**: Mirror traffic based on request path (`routes`) and HTTP method (`methods`).
- **Configurable Timeout**: Sets a timeout for mirror requests to prevent resource exhaustion.
- **Customizable Logging**: Control the log level of mirror requests or disable it entirely.
- **Identifier Headers**: Optionally adds `X-Traffic-Mirror` and `X-Original-Host` headers to mirrored requests.

### 1. Compilation

This module is not included in the standard Caddy distribution. You need to build a custom Caddy binary with this module included.

**Prerequisite**: Make sure you have `xcaddy` installed.
```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
```

**Build Steps**:

1.  Clone this repository:
    ```bash
    git clone https://github.com/your-username/caddy-traffic-mirror.git
    ```

2.  Navigate to the project directory:
    ```bash
    cd caddy-traffic-mirror
    ```

3.  Run the build script. This will compile a binary for your current OS and another for Linux (amd64).
    ```bash
    ./build.sh
    ```

After a successful build, you will find two executables in the current directory:
- `caddy`: For your current operating system.
- `caddy-linux`: For Linux (amd64) systems.

### 2. Usage (Caddyfile)

#### Basic Example

In this example, requests to `/api/*` are served by `localhost:8000`, and simultaneously mirrored to `http://localhost:9000`.

```caddyfile
example.com {
    route /api/* {
        # 1. Mirror the traffic first
        traffic_mirror {
            target_url http://localhost:9000
        }

        # 2. Then, handle the original request
        reverse_proxy localhost:8000
    }
}
```

#### Complete Demo

Here is a self-contained `Caddyfile.demo` to test the functionality locally. It runs an origin server on `:8090` and a mirror server on `:9090`.

```caddyfile
# Caddyfile.demo
{
    debug
    admin off

    # Define a custom logger for the mirror server
    logging {
        log mirror_log {
            output file ./mirror.log
            format console
        }
    }
}

# Origin Server
:8090 {
    log {
        output file ./access.log
        format console
    }

    route {
        # For requests to /api/*, mirror them and then respond.
        route /api/* {
            traffic_mirror {
                target_url http://localhost:9090
                methods GET POST
                add_mirror_headers true
                timeout 3
            }

            respond "API from Origin" 200
        }

        # For all other requests, just respond.
        handle  {
            respond "Other path from Origin" 200
        }
    }
}

# Mirror Server
:9090 {
    # Use the custom logger to ensure internal requests are logged.
    log mirror_log

    respond "Received by Mirror Server (9090): {http.request.uri}" 200
}
```

To run this demo:
```bash
./caddy run --config Caddyfile.demo
```
Now, access `http://localhost:8090/api/test`. You will see "API from Origin" in your browser, and the `mirror.log` file will show a new entry for the mirrored request.

### Configuration Options

| Directive          | Description                                                                                             | Example                               |
| ------------------ | ------------------------------------------------------------------------------------------------------- | ------------------------------------- |
| `target_url`       | **(Required)** The base URL for the mirror destination.                                                 | `target_url http://mirror-service`    |
| `routes`           | (Optional) Mirrors only requests whose paths match one of these prefixes.                               | `routes /api/v1 /user/profile`        |
| `methods`          | (Optional) Mirrors only requests whose HTTP methods match.                                              | `methods GET POST`                    |
| `timeout`          | (Optional) Timeout in seconds for the mirror request. Default is `30`.                                  | `timeout 5`                           |
| `add_mirror_headers` | (Optional) If `true`, adds `X-Traffic-Mirror` and `X-Original-Host` headers. Default is `true`.         | `add_mirror_headers false`            |
| `log_level`        | (Optional) Log level for completed mirror requests (`debug`, `info`, `warn`, `error`). Default is `debug`. | `log_level info`                      |
| `disable_log`      | (Optional) If present, disables logging for mirror requests entirely.                                   | `disable_log`                         |

---

## 中文

`caddy-traffic-mirror` 是一个为 Caddy v2 设计的 HTTP 中间件模块，用于将流入的 HTTP 请求复制（或称“镜像”）到指定的后端服务。镜像过程是异步的，不会阻塞或影响原始的请求-响应流程。

这对于流量分析、使用生产数据测试新版本服务、或被动数据收集等场景非常有用。

### 功能特性

- **异步镜像**：不影响原始请求的响应延迟。
- **灵活过滤**：可根据请求路径 (`routes`) 和 HTTP 方法 (`methods`) 筛选要镜像的流量。
- **可配置超时**：为镜像请求设置超时时间，防止资源耗尽。
- **自定义日志**：控制镜像请求的日志级别，或完全禁用它。
- **标识请求头**：可选择性地为镜像请求添加 `X-Traffic-Mirror` 和 `X-Original-Host` 请求头。

### 1. 编译

该模块未包含在 Caddy 的标准发行版中。您需要使用此模块构建一个自定义的 Caddy 程序。

**前置条件**：请确保您已安装 `xcaddy`。
```bash
go install github.com/caddyserver/xcaddy/cmd/xcaddy@latest
```

**编译步骤**：

1.  克隆本仓库：
    ```bash
    git clone https://github.com/your-username/caddy-traffic-mirror.git
    ```

2.  进入项目目录：
    ```bash
    cd caddy-traffic-mirror
    ```

3.  运行构建脚本。该脚本会为您的当前操作系统和 Linux (amd64) 分别编译一个可执行文件。
    ```bash
    ./build.sh
    ```

成功构建后，您会在当前目录下找到两个可执行文件：
- `caddy`: 用于您的当前操作系统。
- `caddy-linux`: 用于 Linux (amd64) 系统。

### 2. 使用方法 (Caddyfile)

#### 基础示例

在此示例中，所有对 `/api/*` 的请求都由 `localhost:8000` 提供服务，并同时被镜像到 `http://localhost:9000`。

```caddyfile
example.com {
    route /api/* {
        # 1. 首先镜像流量
        traffic_mirror {
            target_url http://localhost:9000
        }

        # 2. 然后处理原始请求
        reverse_proxy localhost:8000
    }
}
```

#### 完整演示

这是一个独立的 `Caddyfile.demo` 文件，用于在本地测试所有功能。它在 `:8090` 端口运行一个源服务器，在 `:9090` 端口运行一个镜像服务器。

```caddyfile
# Caddyfile.demo
{
    debug
    admin off

    # 为镜像服务器定义一个自定义的日志记录器
    logging {
        log mirror_log {
            output file ./mirror.log
            format console
        }
    }
}

# 源服务器
:8090 {
    log {
        output file ./access.log
        format console
    }

    route {
        # 对于 /api/* 的请求，先镜像，然后响应
        route /api/* {
            traffic_mirror {
                target_url http://localhost:9090
                methods GET POST
                add_mirror_headers true
                timeout 3
            }

            respond "API from Origin" 200
        }

        # 对于所有其他请求，直接响应
        handle  {
            respond "Other path from Origin" 200
        }
    }
}

# 镜像服务器
:9090 {
    # 使用自定义日志记录器，确保内部请求也能被记录
    log mirror_log

    respond "Received by Mirror Server (9090): {http.request.uri}" 200
}
```

运行此演示：
```bash
./caddy run --config Caddyfile.demo
```
现在，访问 `http://localhost:8090/api/test`。您会在浏览器中看到 "API from Origin"，同时 `mirror.log` 文件中会新增一条镜像请求的日志记录。

### 配置选项

| 指令               | 描述                                                                                              | 示例                                  |
| ------------------ | ------------------------------------------------------------------------------------------------- | ------------------------------------- |
| `target_url`       | **(必需)** 镜像目标的 URL 地址。                                                                  | `target_url http://mirror-service`    |
| `routes`           | (可选) 仅镜像路径匹配这些前缀之一的请求。                                                         | `routes /api/v1 /user/profile`        |
| `methods`          | (可选) 仅镜像匹配这些 HTTP 方法的请求。                                                           | `methods GET POST`                    |
| `timeout`          | (可选) 镜像请求的超时时间（秒）。默认为 `30`。                                                    | `timeout 5`                           |
| `add_mirror_headers` | (可选) 若为 `true`，则添加 `X-Traffic-Mirror` 和 `X-Original-Host` 请求头。默认为 `true`。         | `add_mirror_headers false`            |
| `log_level`        | (可选) 已完成的镜像请求的日志级别 (`debug`, `info`, `warn`, `error`)。默认为 `debug`。              | `log_level info`                      |
| `disable_log`      | (可选) 若存在，则完全禁用镜像请求的日志记录。                                                     | `disable_log`                         |