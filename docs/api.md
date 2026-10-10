# Kairos API

Kairos 的 HTTP、Connect RPC、媒体和前端来自同一个服务，使用实例自己的域名。
接口定义由本仓库维护：[Proto 源文件](../proto/api/v1/)、[生成的 OpenAPI](../proto/gen/openapi.yaml)、[TypeScript 客户端类型](../web/src/types/proto/api/v1/)。

## 认证与使用范围

浏览器登录由应用管理；外部脚本可在设置中创建个人访问令牌，并在请求中设置 `Authorization: Bearer <个人令牌>`。
令牌代表本人权限，应为每个接入单独创建、配置期限并按需撤销；不要在 issue、截图或日志中公开。

普通业务接口位于 `/api/v1/`；具体请求字段、分页、响应与权限以当前 Proto 和 OpenAPI 为准。
完整的新记录保存、附件及分区外发需遵循现有业务接口；不能用数据库直接写入代替保存流程。

## 过滤

列表筛选由本仓库 [filter](../filter/README.md) 编译和校验。前端当前仍有高级表达式入口，易用性改进在 [#25](https://github.com/EgoSay/kairos/issues/25)。

## 兼容性

Go module 为 `github.com/EgoSay/kairos`。既有 `/api/v1/` 路径、`memos.api.v1` RPC 名称、资源标识与导出 MIME 类型保留，以便原客户端和档案继续使用。
仓库独立化没有将旧 API 全部改名，也不代表所有上游客户端已通过兼容性验证。

第三方浏览器扩展和社媒接收服务按其接口逐项验证；Kairos 当前没有自己的官方浏览器扩展。
接口改动通过 `.proto` 和生成流程维护，不手改生成代码。
