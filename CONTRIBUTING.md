# 参与 Kairos

先阅读 [产品理念](BRAND.md) 和 [当前需求](https://github.com/EgoSay/kairos/issues/32)。
Kairos 围绕低负担记录、主动回顾、本人控制和长期数据保障迭代。

## 开发

使用 Go 1.27、Node.js 24 和 pnpm 11.0.1。克隆后：

```sh
cd web
pnpm install --frozen-lockfile
pnpm dev
```

在另一个终端从仓库根目录启动后端：

```sh
go run -buildvcs=true ./cmd/memos --port 8081 --data ./data-dev
```

前端开发入口为 `http://localhost:3001`。`cmd/memos` 和 `MEMOS_*` 是保留的兼容入口，程序的产品名和 Go module 已属于 Kairos。
开发只使用演示数据；不要把私人记录、凭据或生产备份提交到 Git。

## 提交改动

用 issue 描述触发场景、期望结果和验收条件；较大的交互或数据模型改动先对齐范围。
PR 说明具体行为变化、验证和实际限制。涉及品牌或部署入口时，同步 README、帮助链接及相关操作说明。

遵循 [AGENTS.md](AGENTS.md) 中的目录、Proto、迁移和检查约定。修改 `.proto` 后通过 `buf generate` 生成 API，禁止手改生成文件。
修改数据模型时，三个数据库驱动的迁移与新安装结构保持一致。

## 验证

```sh
cd web
pnpm lint
pnpm test
pnpm build
cd ..
go test ./...
```

存储测试使用 Docker/Testcontainers；按改动范围执行 `AGENTS.md` 中的检查。
保存、离线草稿、权限、附件和恢复需要覆盖实际失败与恢复路径，测试通过不替代手机和生产环境验证。

## 发布

普通分支推送和 PR 合并不会部署。`YY.MM[.N][-rc.N]` 标签触发镜像发布与现有生产流程，RC 同样可能部署生产。
具体规则见 [部署说明](docs/operations/deployment.md) 和 [发布 Skill](.agents/skills/release/SKILL.md)。
有意引入数据或接口不兼容时，需要明确迁移和恢复办法。

所有新增贡献按本仓库 [MIT License](LICENSE) 提供；保留第三方版权和来源说明。
