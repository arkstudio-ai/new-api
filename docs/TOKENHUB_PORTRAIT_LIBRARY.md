# TokenHub 真人素材库接入

## 行为与范围

启用的 TokenHub Sora（type 55）渠道在视频预扣费之前准备参考素材：检查归属 → 按文件内容 SHA-256 查找/预留入库记录 → 上传 → 查询至 Active → 替换为 `asset://<Id>` → 提交生成。

仅修改素材引用。模型、角色、顺序、显式 `false` / `0`、时长和分辨率保留。覆盖 `metadata.content[]`、`content[]`、单个 image/video/audio URL 以及 images/extra_images/extra_videos/extra_audios 数组。无参考素材的文生视频不要求资产组。该渠道只接受 Seedance 模型和 JSON 素材 URL，不接受 multipart 上传。

真人需要先由本人完成 H5 认证，认证产生的 LivenessFace 组才可使用。AIGC 组用于虚拟素材，不能替代真人授权。新网关不会自动导入上游账户中已有的其他人像组。

## 渠道配置

在渠道的 `settings` JSON 中加入：

```json
{
  "tokenhub_portrait_enabled": true,
  "tokenhub_portrait_project": "深圳赞卫数字科技有限公司"
}
```

- 渠道类型：55（Sora）。Base URL：`https://tokenhub.moligroup.com`。
- Key：TokenHub Bearer API key；仅单密钥账户，不支持多 Key 轮换。
- models：配置实际开通的 Seedance 2.0 / Fast / 2.5 ID。
- `tokenhub_portrait_project` 为空时采用上面的项目名。客户端的 ProjectName 不能覆盖服务端配置。
- 其他渠道默认关闭，不使用旧火山 AK/SK 接口。
- 首次启动通过 GORM 添加 `token_hub_portrait_sessions/groups/assets` 三张表；使用跨数据库字段和 GORM 查询，无旧表数据变更。

## 认证与生成

以下请求使用调用者自己的 new-api Bearer token。多条 TokenHub 渠道时，用 `?channel_id=<渠道ID>` 选择同一渠道；省略时按当前令牌分组中的启用渠道优先级选择。认证、入库、生成必须使用同一用户、令牌、渠道、项目和上游凭据。

1. `POST /v1/portrait/session`，请求体 `{}` 或 `{"CallbackURL":"https://..."}`，返回 H5Link 和 BytedToken。客户端展示 H5 链接，由本人在手机完成认证。凭证有效期 120 秒，不保存明文凭证。
2. `POST /v1/portrait/validate-result`，请求体 `{"BytedToken":"<本次认证凭证>"}`。每 2–3 秒查询，完成后返回 GroupId 并保存调用者绑定。回调不能作为认证成功的依据。
3. 可选提前入库：`POST /v1/portrait/asset`，请求体如下。返回 Id、Status 和 URI；Processing 时继续 GET 单个素材。

```json
{
  "GroupId": "<认证产生的GroupId>",
  "URL": "https://<公网素材文件URL>",
  "AssetType": "Video"
}
```

4. 生成请求仍为 `POST /v1/videos`，增加 `metadata.portrait_group_id`。示例：

```json
{
  "model": "doubao-seedance-2-0-260128",
  "prompt": "使用参考视频中的已授权人物，保持原有茶室环境。",
  "metadata": {
    "portrait_group_id": "<认证产生的GroupId>",
    "resolution": "720p",
    "ratio": "9:16",
    "duration": 15,
    "generate_audio": true,
    "watermark": false,
    "content": [{
      "type": "video_url",
      "video_url": {"url": "https://<原素材URL>"},
      "role": "reference_video"
    }]
  }
}
```

网关会自行入库和等待；也可以直接传此前获得的 `asset://<Id>`，网关仍校验归属与上游状态。内部字段 portrait_group_id 不转发至上游。

## 管理接口

提供 session、validate-result、asset-group 创建/列表/详情/改名/删除，asset 上传/列表/详情/删除。列表仅返回本调用者本账户的已绑定记录；素材列表需在 `Filter.GroupIds` 中指定一个组。素材列表为本地状态快照，单素材 GET 查询实时上游状态。

自动入库素材的上游 Name 使用确定性标识以恢复未知上传结果，暂不开放素材改名接口。删除是显式管理操作，不在失败、超时、重试或回滚时自动执行。上游删除成功后保留本地失效记录。

TokenHub 实际接口与附带文档的 GET 示例有差异：上游 GET 仍要求 JSON 请求体，且必须包含 Id 和 ProjectName。网关替调用方补齐，因此下游 GET 无需携带请求体。DELETE 采用相同的显式 Id / ProjectName JSON 方式；真实删除尚未联调。

## 复用、超时与收费边界

- 下载素材仅为流式计算 SHA-256，不保存明文源文件或签名 URL；单文件最多 100 MiB，下载最多 30 秒，限制公网 HTTP/HTTPS，连接时校验解析出的实际 IP，重定向同样校验。
- 缓存范围包含调用者用户/令牌、渠道、上游凭据指纹、项目、资产组、素材类型和内容摘要。更换签名但内容相同可以复用，内容改变必须重新入库。密钥更换需重新绑定，不能跨账户复用。
- 数据库唯一预留记录保证多进程并发不会重复创建同一素材。上传结果不明或进程中断后，按所在组及确定性 Name 查询恢复。查询不到时返回待人工核对，不能盲目再上传。
- 素材准备总时限 55 秒。Processing 超时保留已获得的素材 ID，后续请求继续查询。Failed、已删除、归属不匹配都阻止生成。
- 前置失败返回 HTTP 400 和具体 `portrait_*` code，兼容现有视频客户端的“明确未受理”分类；素材管理 API 自身使用 400/404/409/410/422/429 等状态。
- 素材准备前置失败不进入 new-api 视频预扣费。素材库自身是否收费遵循上游条款，不能由视频未扣费推断素材服务免费。
- 启用渠道不自动跨渠道重试，包括生成请求结果不明的情况，避免资产账户错配或重复生成。
- 活体创建/结果查询各 3 QPS，列表 10 QPS，查询合计 100 QPS，更新 10 QPS，删除合计 5 QPS。Redis 模式跨副本共享限流，未启用 Redis 时仅进程内共享；CreateAsset 权益额度由上游控制。

## TokenHub 视频状态兼容

- 初期 `unknown` 当作进行中。
- 完成结果读取 `metadata.url`，缺少链接继续查询。
- 下载结果使用成片 URL，不把 TokenHub Bearer key 带到外部素材域名。
- 已知真人隐私拦截返回 portrait_input_privacy_rejected，不再统称参数错误。

## 上线顺序与当前验证

1. 本地测试与代码审核完成后，备份渠道配置和数据库，发布 new-api；先保持业务流量原路由。
2. 启用独立 TokenHub 渠道，用调用方 token 完成本人认证与单份素材入库，核验 Asset URI 在该账户可用。
3. 经实际出片验收，再将 OpenBox 的 Seedance provider 从直连 TokenHub 改为自有 new-api，并传递已绑定的 portrait_group_id。未传组 ID 的参考生成会明确拒绝，不能直接全量切换。
4. 回滚可恢复 OpenBox 原路由；已有任务完成前保留 TokenHub 渠道及兼容设置，保留所有认证/素材记录，不自动删除上游资源。

已完成：本地素材库状态机、HTTP 仿真、并发/竞态、认证隔离、原参数保留及生成前计费边界验证；真实 TokenHub 素材组列表可读，GET 的 JSON / Id 要求已做只读核验。

尚未完成：本人 H5 认证、真实素材入库与 Asset URI 出片、真实 DELETE、生产部署及 OpenBox 路由切换。没有发起付费生成。

## 测试

```sh
go test -race ./service/tokenhubportrait ./relay/channel/task/sora ./controller ./router ./relay \
  -run 'Portrait|TokenHub|PublicTaskIDs|SourceURL|GenerationError' -count=1
go test ./model ./service ./relay/channel/task/... -count=1
go vet ./service/tokenhubportrait ./relay/channel/task/sora ./controller ./router ./relay
```

本分支的全仓回归已执行；干净 main 同样存在模型列表测试和 Claude 文件内容转换测试失败。根包还需要先构建 web/classic/dist 与 web/default/dist 才能满足 go:embed。上述限制不应报告为全仓测试通过。
