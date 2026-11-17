# 跨包精确小改

格式：`PI-x | 文件 | 改了什么 | 原因`

- PI-2 | `relay/relay_task.go`（第 102 行附近） | remix 锁定原任务渠道时 `ch.GetNextEnabledKey()` 改为 `ch.GetNextEnabledKeyFor(info.OriginModelName, info.UserId)` | 转发路径上的选 key 必须按模型与用户过滤，绑定 key 不能被其他用户选到（design §8）。
- PI-2 | `model/model_meta.go`（第 326 行附近） | 批量从渠道移除模型时，`Select("id","models","status","group","priority","weight","tag")` 补上 `channel_info`、`settings` 两列 | 该处随后调用 `channel.UpdateAbilities(tx)`，abilities 推导现在依赖 `PublicModels()`（channel_info）与 `settings.group_models`；缺列会把绑定 key 当公共 key、把分组白名单当空，生成错误的 abilities。
