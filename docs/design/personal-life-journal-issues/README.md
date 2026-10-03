# 私人生活记录工具：开发议题与需求文档

> 后续状态（2026-10-03）：用户已授权按议题开发。以下保留创建议题时的记录；当前工程实现与外部验收条件见 [验收记录](../../operations/personal-journal-validation.md)，运行方式见 [运行说明](../../operations/personal-journal.md)。

- 需求基线：[产品需求与交互规范 v0.2](../personal-life-journal-prd.md)
- 目标仓库：<https://github.com/EgoSay/memos>，只在本 fork 建立议题。
- 当前发布状态：2026-10-03 已在 GitHub 发布 18 条 open issues（1 条总议题、17 条分项），并逐条读回核对标题、完整正文与状态。
- 当前工作范围：建立议题与需求描述；尚未开始产品实现，具体默认值评审状态以总需求为准。
- 总议题已保存完整 PRD，各分项正文即在线需求文档。本目录保留与远端正文一致的 Markdown；本轮未向 main 提交文件。

## 总议题

[GitHub 总议题 #1：完整 PRD 与开发清单](https://github.com/EgoSay/memos/issues/1) · [本地正文](master.md)

## 分项议题

| 稳定编号 | 需求描述 | 阶段 | 前置依赖 | GitHub |
| --- | --- | --- | --- | --- |
| J01 | [需求评审与低保真交互原型](j01.md) | 先行评审 | 无 | [#2](https://github.com/EgoSay/memos/issues/2) |
| J02 | [日常记录、时间语义与基础找回](j02.md) | 记录基础 | J01 | [#3](https://github.com/EgoSay/memos/issues/3) |
| J03 | [照片与录音原件、可选转写](j03.md) | 记录基础 | J01、J02 | [#4](https://github.com/EgoSay/memos/issues/4) |
| J04 | [草稿持久化、离线保存与同步冲突恢复](j04.md) | 记录基础 | J02、J03 | [#5](https://github.com/EgoSay/memos/issues/5) |
| J05 | [编辑版本、最近删除与衍生内容清理](j05.md) | 记录基础 | J02 | [#6](https://github.com/EgoSay/memos/issues/6) |
| J06 | [保留活动日历数量着色与日期浏览](j06.md) | 记录基础 | J02、J05 | [#7](https://github.com/EgoSay/memos/issues/7) |
| J07 | [个人分区及安全的归属与删除语义](j07.md) | 组织与外发基础 | J01、J02 | [#8](https://github.com/EgoSay/memos/issues/8) |
| J08 | [每日回顾：主动进入、同日稳定的旧记录](j08.md) | 自愿探索 | J02、J05 | [#9](https://github.com/EgoSay/memos/issues/9) |
| J09 | [随机漫步：纯随机、可后退、随时离开](j09.md) | 自愿探索 | J08 | [#10](https://github.com/EgoSay/memos/issues/10) |
| J10 | [AI 洞察：手选来源、可核对、原文不变](j10.md) | 自愿探索 | J02、J05、J08 | [#11](https://github.com/EgoSay/memos/issues/11) |
| J11 | [自定义集合分享：时期、标签、手选与快照](j11.md) | 自主分享 | J02、J05、J07 | [#12](https://github.com/EgoSay/memos/issues/12) |
| J12 | [访客只读页面、分享授权及媒体访问边界](j12.md) | 自主分享 | J11、J03 | [#13](https://github.com/EgoSay/memos/issues/13) |
| J13 | [分区 Webhook 配置、自动外发触发与可见状态](j13.md) | 自主同步 | J07、J04、J05 | [#14](https://github.com/EgoSay/memos/issues/14) |
| J14 | [Webhook 持久投递、去重、顺序与授权复核](j14.md) | 自主同步 | J13 | [#15](https://github.com/EgoSay/memos/issues/15) |
| J15 | [首个社媒目标接入与真实发布链路验证](j15.md) | 自主同步 | J03、J13、J14 | [#16](https://github.com/EgoSay/memos/issues/16) |
| J16 | [可迁移导出、独立备份与无意外外发的恢复](j16.md) | 长期数据保障 | J03、J04、J05、J07 | [#17](https://github.com/EgoSay/memos/issues/17) |
| J17 | [完整版本验收：记录体验、隐私与跨功能恢复](j17.md) | 整体验收 | J04、J05、J06、J08、J09、J10、J11、J12、J13、J14、J15、J16 | [#18](https://github.com/EgoSay/memos/issues/18) |

## 验收覆盖

| PRD 验收编号 | 主要负责议题 |
| --- | --- |
| AC01 | J02 |
| AC02 | J03 |
| AC03 | J01、J02 |
| AC04 | J01、J02、J08、J09 |
| AC05 | J08 |
| AC06 | J08 |
| AC07 | J09 |
| AC08 | J08、J09 |
| AC09 | J10 |
| AC10 | J10 |
| AC11 | J10 |
| AC12 | J10 |
| AC13 | J10 |
| AC14 | J05、J10 |
| AC15 | J08、J09 |
| AC16 | J04 |
| AC17 | J04 |
| AC18 | J04 |
| AC19 | J05 |
| AC20 | J16 |
| AC21 | J16 |
| AC22 | J01、J02 |
| AC23 | J05、J10、J12 |
| AC24 | J11 |
| AC25 | J11 |
| AC26 | J11 |
| AC27 | J12 |
| AC28 | J07、J13 |
| AC29 | J13 |
| AC30 | J14、J15 |
| AC31 | J13、J14 |
| AC32 | J13、J14、J15 |
| AC33 | J07 |
| AC34 | J06 |
| AC35 | J06 |
| AC36 | J14、J16 |
| AC37 | J15 |

J17 对 AC01–AC37 进行整体复核，不替代各功能议题的证据。未选择的设备、服务和平台保留为明确的技术设计输入，不编造选择结果。

## 发布记录与验证

- 目标为 `EgoSay/memos`，未向上游仓库发布。
- 上轮失败由会话审批策略 `never` 拒绝写入造成；用户已开启 Issues。本轮 `gh` 网络及管理员权限验证通过，成功完成创建。
- 远端共 18 条本产品议题，均为 open；标题和完整正文已逐字读回核对，稳定标识唯一，总清单及依赖已使用实际链接。
- 分项覆盖 AC01–AC37；本地链接、正文长度和空白检查通过。无应用代码修改、运行测试、提交或部署。
- 未通过创建 issue 将建议默认值自动记为产品决议；产品评审和后续开发仍按总需求范围推进。
- 恢复发布时依据正文中的 `personal-life-journal:MASTER/Jxx` 稳定标识去重；已创建内容应复用，不能重复创建。
