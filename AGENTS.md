# AGENTS

这个文件是仓库内的长期工作记忆。后续在这个项目里继续开发时，优先先读这里，再动代码。

## 项目目标

- 这是一个 OpenAI 兼容的图片网关。
- 当前后端固定转发到 Labnana 的 `gpt-image-2` 2K 生图接口。
- 管理页负责 API Key 输入、Labnana key 管理、配置摘要和最近图片画廊。

## 当前架构

- 程序入口：`cmd/labnana2api/main.go`
- HTTP 服务与 OpenAI 兼容接口：`internal/server/`
- 配置读取与热更新：`internal/config/`
- Labnana 上游调用：`internal/labnana/`
- 代理感知 HTTP 客户端：`internal/httpclient/`
- 对象存储：`internal/storage/`
- 本地画廊元数据：`internal/gallery/`
- 管理页内嵌资源：`internal/web/`

## 管理页前端约定

- 管理页资源保持拆分，不要回退成一个超长单文件。
- 当前拆分方式：
- `internal/web/index.html`：页面骨架
- `internal/web/base.css`：基础视觉和布局
- `internal/web/console.css`：表单、列表、画廊等管理台组件
- `internal/web/app.js`：管理页交互逻辑
- `internal/web/embed.go`：资源内嵌与静态分发

## Web 设计风格

- 管理页视觉以 `/opt/grok2api` 的管理台风格为参考。
- 使用浅色背景、黑白主色、Geist 字体、细边框、小半径和克制阴影。
- 优先做“极简管理控制台”，不要再做大面积玻璃态、厚重渐变、过度装饰卡片。
- 信息层级要清楚：顶部概览、认证、配置、Key 管理、画廊，顺序稳定。
- 空状态必须明确可见，不能留出大片空白不解释。
- 管理台按钮风格保持克制：主按钮黑底白字，次按钮白底细边框。

## 代码约束

- 代码要有适量中文注释，重点解释不直观的约束、边界和安全原因，不要写废话注释。
- 单个代码文件不能超过 300 行。
- 如果超过 300 行，必须按功能拆分后再继续开发。
- Go 大文件优先按职责拆分，例如 `server_keys.go`、`server_gallery.go`、`server_images.go` 这样的形式。
- 前端资源也遵守同样规则，CSS/JS/HTML 不要堆成一个超长文件。

## 当前技术债提醒

- `internal/server/server.go` 当前已经明显超过 300 行。
- 下次如果继续修改服务端路由、鉴权、Key 管理、画廊或图片接口逻辑，优先先拆分这个文件，再追加功能，不要继续把逻辑堆进同一个文件。

## 开发与启动

- 首次生成配置：`./scripts/generate-config.sh`
- 启动服务：`./scripts/start.sh`
- 查看状态：`./scripts/status.sh`
- 查看日志：`./scripts/logs.sh`
- 查看 stdout 重定向日志：`./scripts/logs.sh stdout`
- 停止服务：`./scripts/stop.sh`
- `scripts/start.sh` 会在缺少 `config.json` 时尝试从 `config.example.json` 生成一份。

## 开发时的同步要求

- 如果项目结构、启动命令、截图流程、设计规则、关键接口有变化，需要在同一轮改动里同步更新 `README.md` 和 `AGENTS.md`。
- 如果新增了新的管理页资源文件，也要同步更新这里的“管理页前端约定”。
- 如果启动方式变化，必须同步更新这里的“开发与启动”。

## 截图更新流程

- 不要再使用会卡住的图片查看路径或依赖 `view_image`。
- 管理页截图输出位置固定为：
- `docs/screenshots/admin-desktop.png`
- `docs/screenshots/admin-mobile.png`
- 更新截图前先确保服务已经用最新代码重新启动。
- 截图时给浏览器预置 `localStorage` 中的 `labnana2api-admin-key`，保证页面能自动带上 Bearer 头加载配置和 Key 列表。
- 当前可用方案是 Playwright CLI，例如：

```bash
tmp_storage=$(mktemp)
cat > "$tmp_storage" <<'EOF'
{
  "cookies": [],
  "origins": [
    {
      "origin": "http://127.0.0.1:18082",
      "localStorage": [
        {
          "name": "labnana2api-admin-key",
          "value": "labnana2api"
        }
      ]
    }
  ]
}
EOF

npm exec --yes --package=playwright playwright -- screenshot -b chromium --channel=chrome \
  --viewport-size="1440,1800" --wait-for-timeout=2500 --full-page \
  --load-storage="$tmp_storage" http://127.0.0.1:18082/ docs/screenshots/admin-desktop.png

npm exec --yes --package=playwright playwright -- screenshot -b chromium --channel=chrome \
  --viewport-size="430,2000" --wait-for-timeout=2500 --full-page \
  --load-storage="$tmp_storage" http://127.0.0.1:18082/ docs/screenshots/admin-mobile.png

rm -f "$tmp_storage"
```

## 提交节奏

- 每完成一个可独立 review 的阶段，就及时提交一次，不要把前端、后端、文档、截图全混成一笔大提交。
- 建议至少按这类粒度拆分提交：
- 前端界面/交互
- 后端接口或存储逻辑
- README、AGENTS、截图等文档与资产
- 提交前至少做与改动对应的基础验证，例如 `go test ./...`、服务重启、页面截图刷新。
