# Kairos 部署、升级与恢复

本文维护 Kairos 的当前部署入口。主应用由 Go 服务内嵌 React 构建，数据库和附件必须持久化。

## 本机或单机起步

使用仓库根目录 [README](../../README.md#快速开始) 的源码构建方式。`scripts/compose.yaml` 只绑定本机 5230 端口，使用自己的 `kairos-data` 卷；浏览器第一次进入时创建管理员。

可通过 `KAIROS_PORT` 调整宿主端口，通过 `KAIROS_TIMEZONE` 调整显示时区。
构建时传入 `KAIROS_VERSION` 和 `KAIROS_COMMIT`，将版本与源码对应起来。
容器内部继续使用 `/var/opt/memos` 和 `MEMOS_*`，用于现有数据与配置兼容。

开放公网前：完成管理员初始化，检查私密模式和注册设置，配置 HTTPS，再测试匿名访问边界。
持久化卷不能代替异地备份；更新时保留卷，不使用 `down --volumes`。

## 自有镜像与 Dokploy

镜像仓库为 `ghcr.io/egosay/kairos`。当前生产构建目标为 `linux/amd64`；其他架构可从源码构建，并自行验证。
部署固定的 `sha256` digest，镜像可拉取且经过检查后才更新声明。

现有流程：

1. 推送符合 `YY.MM[.N][-rc.N]` 的版本标签。
2. GitHub Actions 运行前后端检查，构建完整镜像，并做启动、持久化和升级验证。
3. 升级闸门完成一致性快照和远端加密备份。
4. 更新 `production/memos-journal` 分支的 `scripts/compose.production.yaml` 和版本记录。
5. Dokploy 的 GitHub App 收到该分支事件，拉取固定 digest；检查公网源码身份与实际容器健康。
6. 确认后恢复备份调度，保留发布回执。

普通分支推送和 PR 合并不部署；RC 标签也走生产流程。详情见 [发布与恢复工具](../../scripts/README.md)。

新建 Dokploy 环境可参考 `scripts/compose.dokploy.yaml`。生产声明使用固定 digest；模板里的镜像变量不能覆盖生产声明。
镜像名称改变时，必须同时核对：GitHub App 的仓库、Dokploy repository/owner/branch/composePath、实际 Compose 镜像、镜像拉取权限、发布闸门允许的镜像仓库，以及回滚声明。

## 兼容与迁移

| 项目 | Kairos 的处理 |
| --- | --- |
| 代码仓库、帮助和反馈 | `EgoSay/kairos` |
| Go module | `github.com/EgoSay/kairos` |
| 新镜像 | `ghcr.io/egosay/kairos` |
| 现有 Dokploy appName / composeId | 保留，同一个应用继续挂载原数据 |
| 现有生产服务名和数据卷 | 保留 `memos`、`memos-data` 及实际 Compose 项目前缀 |
| 生产声明分支 | 保留 `production/memos-journal`，避免同时改变发布触发和数据绑定 |
| 现有域名、Tunnel 和备份路径 | 保留，由运维按实际资源维护；不随品牌改名迁移私人数据 |
| 数据目录与数据库名 | 保留 `/var/opt/memos` 和既有数据库文件名 |
| 环境变量、API、档案格式 | 保留 `MEMOS_*`、既有 RPC/HTTP 名称与 MIME 类型 |

新安装可以用 `kairos-data`；已有实例不能直接套用新安装 Compose，否则可能创建新的空卷。
镜像来源切换本身不需要复制数据库或清空浏览器草稿。涉及卷或域名的后续迁移必须另做数据和本机缓存迁移。

GitHub 改名与退出 fork 网络是两个操作。Git 历史、issues、PR 和部署绑定均需保护；没有确认元数据保留能力前，不直接执行不可逆的 fork 脱离。

## 数据保障

[个人可迁移档案与实例恢复](personal-journal.md) 分别服务于带走内容和恢复整套实例。
现有 SQLite 快照脚本将数据库与引用的本地媒体组成一个可校验快照；外部媒体不在它的完整保护范围内。
MySQL/PostgreSQL 使用各自的一致性备份机制，并配套附件快照。

自动备份使用 restic 加密上传到 R2/S3 兼容目的地。需要独立配置调度、凭据、保留策略和监控，不能仅凭创建 bucket 就判断成功。
成功标准包括：定时任务真实运行、远端快照完成、逐文件校验，以及隔离恢复后的账户、记录和媒体可读取。
备份上报失败和备份本身失败分别判断。尚未同步的浏览器草稿不在服务器备份内。

恢复密钥应在服务之外保留独立副本。恢复到新的隔离目录，暂停旧分享和外发授权，验证后再计划切换；不直接覆盖正在写入的数据库。
现有脚本、配置字段与检查命令详见 [scripts/README.md](../../scripts/README.md)。

## 已知限制

- 产品内 S3 配置、检测和备份状态页面在 [#31](https://github.com/EgoSay/kairos/issues/31) 跟进。
- 发布回滚的容器缺失和声明恢复问题分别在 [#21](https://github.com/EgoSay/kairos/issues/21)、[#23](https://github.com/EgoSay/kairos/issues/23) 跟进。
- 历史部署成功只说明当时结果。每次发布都需要读取当前配置、备份回执和运行版本。
