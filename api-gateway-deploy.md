# API Gateway 部署准备报告

> 纯后端项目（单 exe + SQLite WAL + Vue 仪表盘）。本轮只做整体核查 + 部署准备，**未改任何业务代码**。

## 一、最终核查结果（全部通过）
| 项 | 命令 | 结果 |
|---|---|---|
| 构建 | `go build -o ai-gateway.exe .` | OK（16,272,384 B，Jul 10） |
| 静态检查 | `go vet ./...` | OK，无告警 |
| 单元测试 | `go test ./...`（清 HTTP_PROXY/HTTPS_PROXY） | 全部 `ok`：adapters / api / proxy / storage |
| 运行健康 | `GET /health` | `200`，返回合法状态 JSON |
| 代码改动 | 上一轮 fix3 已实现并验证 | Cohere v2 tool_calls、Gemini 非前缀兜底、Responses 流式 cb/rl 预检 |

> 测试必须在**清除代理环境变量**下跑：主机 shell 残留死代理 `HTTP_PROXY=127.0.0.1:10808`，会路由到死地址导致上游 ECONNREFUSED。命令前缀 `env -u HTTP_PROXY -u HTTPS_PROXY`。

## 二、本次部署准备动作
1. **DB 备份**：`data/gateway.db` → `data/gateway.db.bak-20260710`（部署前保险，未改动原库）。
2. **启动脚本**：新增 `start.bat`，先 `set HTTP_PROXY=`/`HTTPS_PROXY=` 等清代理，再 `ai-gateway.exe`。部署/重启一律走它，规避代理坑。
3. **用新二进制干净重启**：`taskkill` 旧进程 → 启动新 `ai-gateway.exe` → 健康检查 200。
4. 当前运行实例即本次待部署工件（同源重建）。

## 三、部署清单（上线步骤）
- [ ] 拷贝 `ai-gateway.exe` + `data/`（含 `gateway.db` 与既有 `.bak-*` 备份）+ `start.bat` 到目标机。
- [ ] 目标机执行 `start.bat`（不要直接双击 exe，否则可能继承坏代理环境）。
- [ ] 确认 `GET /health` 返回 200。
- [ ] **手动在仪表盘重新填入 OpenAI key**（当前 DB 中 key 为空；其余 provider / 配置原样保留）。
- [ ] 如需自定义监听地址/端口，编辑 `start.bat` 中 `GATEWAY_ADDR` 并改 `ai-gateway.exe` 启动参数（若程序支持）。

## 四、回滚方案
- 停进程：`taskkill -F -IM ai-gateway.exe`
- 恢复库：`copy data\gateway.db.bak-20260710 data\gateway.db`（会丢失备份点之后在仪表盘的改动，按需）
- 用上一版 exe 重启（如有保留）。

## 五、已知边界（非阻断，部署后观察）
- Auto 模型回退：按 provider+model 去重、跳过空 key / 熔断 / 限流（fix2 已加固）。
- 流式中途失败无法回退（header 已发，固有行为）。
- Cohere 工具调用非流式 + 流式均已映射；真实多轮 tool 调用建议上线后用真实 key 冒烟一次。
