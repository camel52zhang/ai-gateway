# AI Gateway — VPS 部署指南（Linux x86_64 + Nginx + TLS）

> 适用场景：把本地开发好的 `ai-gateway`（Go 单二进制 + SQLite + 内置 Vue 仪表盘）
> 部署到一台 Linux x86_64 VPS，域名访问 + HTTPS。
> 本文对应的部署工件已生成在项目根目录：
> - `ai-gateway-linux-amd64` —— 交叉编译好的 Linux 二进制
> - `start.sh` —— Linux 启动脚本（清代理环境变量）
> - `ai-gateway.service` —— systemd 服务单元
> - `nginx-ai-gateway.conf` —— Nginx 反代配置（certbot 前版本）

---

## 一、本地已为你做好的事

| 工件 | 说明 |
|------|------|
| 交叉编译 `ai-gateway-linux-amd64` | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build`，纯 Go SQLite 驱动，无需 VPS 装任何依赖 |
| `start.sh` | 启动前 `unset` 所有代理环境变量，避免上游 ECONNREFUSED |
| `ai-gateway.service` | systemd 托管，崩溃自启、开机自启 |
| `nginx-ai-gateway.conf` | 反代到 `127.0.0.1:7000`，SSE 流式友好 |

数据库 `data/gateway.db` **不进 git**，需单独随 `data/` 目录一起传到 VPS（保留你已配置的 providers 等）。

---

## 二、VPS 上的部署步骤

### 0. 前置条件（在 VPS 上）
```bash
# Debian/Ubuntu 示例
sudo apt update
sudo apt install -y nginx certbot python3-certbot-nginx
```
- 域名已解析到本机公网 IP（A 记录）。
- 防火墙只放行 `22 / 80 / 443`；**不要**放行 `7000`（网关虽监听 0.0.0.0:7000，但只让本机 nginx 访问，外网走 443）。

### 1. 传文件到 VPS
在本机（项目目录）执行，把二进制、启动脚本、data 目录传过去：
```bash
# 假设 VPS 地址为 user@your-vps-ip，目标目录 /opt/ai-gateway
rsync -avz --exclude='*.db-*' \
  ai-gateway-linux-amd64 start.sh data/ \
  user@your-vps-ip:/opt/ai-gateway/

# 同时把 nginx 配置和 service 文件传上去（可选，也可手动创建）
scp nginx-ai-gateway.conf user@your-vps-ip:/tmp/
scp ai-gateway.service   user@your-vps-ip:/tmp/
```
> 注意：传 `data/` 会带上 `gateway.db`（含你的 providers 配置）。OpenAI key 目前在库里是**空的**，上线后需到仪表盘补填。

### 2. 在 VPS 上安放文件并建用户
```bash
ssh user@your-vps-ip
sudo useradd -r -s /usr/sbin/nologin ai-gateway
sudo chown -R ai-gateway:ai-gateway /opt/ai-gateway
sudo chmod +x /opt/ai-gateway/start.sh /opt/ai-gateway/ai-gateway-linux-amd64
```

### 3. 注册 systemd 服务
```bash
sudo cp /tmp/ai-gateway.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now ai-gateway
sudo systemctl status ai-gateway   # 应显示 active (running)
curl -s http://127.0.0.1:7000/health   # 应返回 200
```

### 4. 配置 Nginx + 申请 TLS 证书
```bash
# 替换域名占位符
sudo sed -i 's/__DOMAIN__/gw.example.com/' /tmp/nginx-ai-gateway.conf   # 改成你的真实域名
sudo cp /tmp/nginx-ai-gateway.conf /etc/nginx/sites-available/ai-gateway.conf
sudo ln -s /etc/nginx/sites-available/ai-gateway.conf /etc/nginx/sites-enabled/
sudo nginx -t && sudo systemctl reload nginx

# 申请并自动配置 HTTPS（certbot 会改写本配置，加上 443 + 证书 + 80→443 跳转）
sudo certbot --nginx -d gw.example.com
```

### 5. 验证
```bash
curl -sI https://gw.example.com/health     # HTTP/2 200
# 浏览器打开 https://gw.example.com/login  → 仪表盘
```
登录后在 **Settings / Providers** 里把 OpenAI key 重新填好并保存（库里目前为空）。

---

## 三、日常运维

| 操作 | 命令 |
|------|------|
| 看日志 | `sudo journalctl -u ai-gateway -f` |
| 重启服务 | `sudo systemctl restart ai-gateway` |
| 更新二进制 | 本地重新交叉编译 → rsync 覆盖 → `sudo systemctl restart ai-gateway` |
| 证书续期 | `sudo certbot renew`（certbot 已写入 systemd timer，自动续） |
| 停服 | `sudo systemctl stop ai-gateway` |

---

## 四、回滚方案

更新二进制前先备份：
```bash
cp /opt/ai-gateway/ai-gateway-linux-amd64 /opt/ai-gateway/ai-gateway-linux-amd64.bak
cp -r /opt/ai-gateway/data /opt/ai-gateway/data.bak-$(date +%Y%m%d)
```
回滚：恢复旧二进制 + 旧 `data/` → `sudo systemctl restart ai-gateway`。

---

## 五、安全要点（请务必遵守）
- **7000 端口不要对公网开放**，只允许本机（nginx）访问；公网只走 443。
- 仪表盘务必要有强密码（登录页 `/login`）；若网关暴露明文 HTTP，密钥与聊天内容会裸奔。
- 证书用 Let's Encrypt 免费签发，90 天自动续，无需人工干预。
- SQLite 用 WAL 模式，单进程访问即可；不要多个网关实例共用同一个 `data/gateway.db`。
