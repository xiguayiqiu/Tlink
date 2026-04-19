# Tlink - 简易文件传输工具

一个简单、高效的文件传输工具，支持局域网传输、远程传输和文件下载功能。

**作者：弈秋忘忧白帽**

**版本：tlink-v0.1**

## 功能特性

### 🚀 核心功能

- **局域网文件传输** - 自动发现局域网设备，快速传输文件
- **远程文件传输** - 使用 TCP 协议，支持跨网络传输
- **交互式会话** - 连接后提供命令行界面，支持双向传输
- **文件下载** - 支持多线程下载、断点续传、代理等功能

### ✨ 高级特性

- **文件完整性验证** - 使用 SHA256 哈希验证文件传输完整性
- **进度显示** - 实时显示传输进度和速度
- **优雅取消** - 按 Ctrl+C 取消当前传输，不影响连接
- **系统命令集成** - 在交互式会话中直接执行系统 Shell 命令

### 🔽 下载功能特点

- **多线程下载** - 默认 4 线程，可自定义线程数
- **断点续传** - 下载中断后可从断点继续下载
- **代理支持** - 支持 HTTP/HTTPS 代理和 SOCKS5 代理
- **实时进度** - 显示总进度和每个线程的独立进度
- **自动重试** - 下载失败自动重试（最多 5 次）
- **文件验证** - 下载完成后自动计算 SHA256 哈希验证完整性
- **智能续传** - 自动检测已下载的部分，避免重复下载
- **自动命名** - 从 URL 或响应头自动提取文件名

## 快速开始

### 安装

```bash
git clone https://github.com/xiguayiqiu/Tlink.git
cd tlink
go build -o tlink .
```

### 局域网传输

> 在termux中运行tlink之后无法执行termux命令，这是shellexec导致的兼容问题！

#### 发送方

```bash
# 发送文件
./tlink send <文件路径>

# 示例
./tlink send photo.jpg
./tlink send document.pdf
```

#### 接收方

```bash
# 接收文件（默认保存到当前目录）
./tlink receive

# 或指定保存目录
./tlink receive -o /path/to/save
```

### 远程传输（TCP 模式）
### 远程传输要求

- 必须搭建一个TCP隧道服务器获取一个公网IP和一个端口。
  - 可以使用樱花映射服务（如NAT-PMP、UPnP等）来获取公网IP和端口。
- 你也可以在服务器上使用（如云服务器），无需搭建隧道服务器。

#### 场景 1：发送方监听，接收方连接

```bash
# 发送方 - 监听端口 9000
./tlink send -R :9000

# 接收方 - 连接到发送方
./tlink receive -R <发送方IP>:9000
```

#### 场景 2：接收方监听，发送方连接

```bash
# 接收方 - 监听端口 9000
./tlink receive -R :9000

# 发送方 - 连接到接收方
./tlink send -R <接收方IP>:9000
```

连接成功后，会进入交互式会话界面：

```
[tlink]>
```

### 交互式会话命令

| 命令 | 说明 |
|------|------|
| `send <文件路径>` | 发送文件到对方 |
| `help` / `--help` | 显示帮助信息 |
| `exit` / `quit` | 退出会话 |
| `<其他命令>` | 直接执行系统 Shell 命令 |

示例：

```bash
# 查看当前目录
[tlink]> ls

# 发送文件
[tlink]> send photo.jpg

# 退出
[tlink]> exit
```

### 文件下载

```bash
# 基本下载
./tlink download <URL>

# 指定保存路径
./tlink download <URL> -o /path/to/save

# 示例
./tlink download https://example.com/file.zip
```

### 查看版本

```bash
# 查看版本和作者信息
./tlink --version

# 或使用短参数
./tlink -v
```

## 命令详解

### send - 发送文件

```bash
./tlink send [文件路径] [flags]
```

| 标志 | 说明 |
|------|------|
| `-R, --remote <地址>` | 远程模式，指定监听地址或连接地址 |

### receive - 接收文件

```bash
./tlink receive [flags]
```

| 标志 | 说明 |
|------|------|
| `-o, --output <目录>` | 指定文件保存目录（默认当前目录） |
| `-R, --remote <地址>` | 远程模式，指定监听地址或连接地址 |

### download - 下载文件

```bash
./tlink download <URL> [flags]
```

| 标志 | 说明 |
|------|------|
| `-o, --output <路径>` | 指定保存路径 |
| `-t, --threads <数量>` | 指定下载线程数（默认 4） |
| `-c, --continue` | 启用断点续传 |
| `-e, --proxy <地址>` | 指定代理地址（支持 HTTP/HTTPS 和 SOCKS5） |

#### 代理地址格式

- HTTP 代理: `http://127.0.0.1:8080`
- HTTPS 代理: `https://127.0.0.1:8080`
- SOCKS5 代理: `socks5://127.0.0.1:1080`
- 简写: `127.0.0.1:8080`（自动检测协议）

#### 下载示例

```bash
# 基本下载
./tlink download https://example.com/file.zip

# 指定保存路径
./tlink download https://example.com/file.zip -o /path/to/save/file.zip

# 使用 8 线程下载
./tlink download https://example.com/large.iso -t 8

# 断点续传
./tlink download https://example.com/large.iso -c

# 使用代理下载
./tlink download https://example.com/file.zip -e http://127.0.0.1:8080

# 综合使用
./tlink download https://example.com/large.iso -t 8 -c -e socks5://127.0.0.1:1080
```

## 使用示例

### 局域网文件分享

```bash
# 发送方
$ ./tlink send video.mp4
INFO  等待接收方连接...
INFO  连接成功，开始发送文件: video.mp4 (1.2 GB)
发送进度 | 10.5 MB/s [████████████░░░░░░░░░░]  50% | 58s

# 接收方
$ ./tlink receive
INFO  发现发送方，正在连接...
INFO  正在接收文件: video.mp4 (1.2 GB)
接收进度 | 10.5 MB/s [████████████░░░░░░░░░░]  50% | 58s
```

### 远程双向传输

```bash
# 服务器端（监听）
$ ./tlink send -R :9000
INFO  等待连接...
INFO  连接成功！
[tlink]>

# 客户端（连接）
$ ./tlink receive -R server.example.com:9000
INFO  正在连接...
INFO  连接成功！
[tlink]> send report.pdf
INFO  正在发送文件: report.pdf (2.5 MB)
发送进度 | 2.5 MB/s [████████████████████████] 100% | 1s
```

### 多线程下载示例

```bash
# 使用 8 线程下载大文件并启用断点续传
$ ./tlink download https://example.com/large.iso -t 8 -c
INFO  正在准备下载...
INFO  文件大小: 4.50 GB | 线程数: 8
总进度 | 12.50 MB/s [██████████━━━━━━━━━━━━━━]  35%
线程 0 [████████████████████████] 100%
线程 1 [████████████████━━━━━━━━]  60%
线程 2 [██████████████━━━━━━━━━━]  50%
线程 3 [████████████━━━━━━━━━━━━]  40%
线程 4 [██████████━━━━━━━━━━━━━━]  35%
线程 5 [████████━━━━━━━━━━━━━━━━]  25%
线程 6 [██████━━━━━━━━━━━━━━━━━━]  20%
线程 7 [████━━━━━━━━━━━━━━━━━━━━]  15%

下载完成!
下载速度: 15.23 MB/s | 耗时: 5分32秒 | 大小: 4.50 GB
✓ 文件完整性验证: SHA256=xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

## 技术栈

- **Go 语言** - 高性能并发编程
- **Cobra** - 命令行框架
- **Pterm** - 终端 UI 组件
- **Shellexec** - 系统命令集成

## 编译说明

### 使用构建脚本

项目提供了自动化构建脚本，可以一键编译所有平台版本：

```bash
./build.sh
```

编译后的文件将生成在 `dist` 目录中。

### 支持的平台

| 文件名 | 平台 | 架构 |
|------|------|
| `tlink-linux-amd64` | Linux | x86_64 |
| `tlink-linux-arm` | Linux | ARMv7 |
| `tlink-linux-arm64` | Linux | ARM64 (aarch64) |
| `tlink-termux-arm64` | Termux | ARM64 (aarch64) |
| `tlink-windows-amd64.exe` | Windows | x86_64 |
| `tlink-windows-arm64.exe` | Windows | ARM64 |

### 从源码编译

```bash
# 克隆项目
git clone https://github.com/xiguayiqiu/Tlink.git
cd Tlink

# 编译当前平台
go build -o tlink .

# 查看版本
./tlink --version
```

## 许可证

MIT License
