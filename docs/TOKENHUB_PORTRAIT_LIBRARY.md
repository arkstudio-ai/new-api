# TokenHub 虚拟人像素材库接入

## 行为与范围

启用的 TokenHub Sora（type 55）渠道按固定顺序处理带参考素材的视频请求：创建或复用 AIGC 虚拟素材组 → 按文件内容 SHA-256 查找/预留入库记录 → 上传 → 查询至 Active → 替换为 `asset://<Id>` → 视频预扣费与提交生成。素材准备失败、仍在 Processing 或结果不明时，不提交视频生成。

仅支持 AIGC 虚拟素材组。已移除 H5 活体认证及结果查询入口，历史真人组不能用于此流程。上传必须使用虚拟素材；入库不能把真人素材变为虚拟人像。

仅修改素材引用。模型、角色、顺序、显式 `false` / `0`、时长和分辨率保留。覆盖 `metadata.content[]`、`content[]`、单个 image/video/audio URL 以及 images/extra_images/extra_videos/extra_audios 数组；这些字段中的参考素材都会前置入库。无参考素材的文生视频不创建素材组。启用的渠道只接受 Seedance 模型和 JSON 素材 URL，不接受 multipart 上传。

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
- `tokenhub_portrait_project` 为空时采用文档明确要求的业务隔离项目 `深圳赞卫数字科技有限公司`；如供应商确认了不同的实际项目名，必须在渠道配置中显式填写。项目被拒绝时直接报错，不自动尝试 `default` 或其他项目。客户端 ProjectName 不能覆盖服务端配置。素材项目必须与实际视频推理所用上游账户的项目一致，参见[火山官方项目隔离说明](https://docs.volcengine.com/docs/ark/private-virtual-avatar-library-guide-preview?lang=zh)。
- 提供的接入文档总说明要求公司中文名，创建示例却使用 `default`。此前在 `default` 成功入库只是诊断结果，不能证明这是正确业务项目，也不能据此把 `default` 设为默认。TokenHub 反馈项目名有误后，已恢复文档业务项目，并保留服务端显式配置能力。
- 其他渠道默认关闭，不使用旧火山 AK/SK 接口。
- 首次启动通过 GORM 添加 `token_hub_portrait_groups/assets` 两张表；字段和查询兼容 SQLite/MySQL/PostgreSQL。升级不删除历史表或上游资源。

## 生成前自动入库

以下请求使用调用者自己的 new-api Bearer token。上传、查询及生成应使用同一用户、令牌、渠道、项目和上游凭据。素材管理 API 支持 `?channel_id=<渠道ID>`；省略时按当前令牌分组中的启用渠道优先级选择。多渠道场景需用独立令牌分组等现有路由配置保证后续生成仍走同一渠道。

生成仍调用 `POST /v1/videos`，直接传虚拟参考素材公网 URL，无需先完成认证或手动提供组 ID：

```json
{
  "model": "doubao-seedance-2-0-260128",
  "prompt": "让参考虚拟人物在茶室中介绍茶文化，保持人物外观一致。",
  "metadata": {
    "resolution": "720p",
    "ratio": "9:16",
    "duration": 15,
    "generate_audio": true,
    "watermark": false,
    "content": [{
      "type": "image_url",
      "image_url": {"url": "https://<虚拟人像PNG文件URL>"},
      "role": "reference_image"
    }]
  }
}
```

网关为当前调用者及上游账户自动创建或复用独立 AIGC 组，上传并等待素材 Active 后，向视频接口发送 `asset://<新素材Id>`。已有且属于本调用者的素材 URI 可直接复用，仍实时检查上游状态。`metadata.portrait_group_id` 可选，用于明确选择已绑定的 AIGC 组；该内部字段不转发至上游。

## 可选提前入库

为了预热人像库，可在生成前调用 `POST /v1/portrait/asset`：

```json
{
  "URL": "https://<虚拟人像PNG文件URL>",
  "AssetType": "Image"
}
```

GroupId 可省略，自动使用默认虚拟素材组，也可传已绑定的 AIGC GroupId。返回 Result.Id、GroupId、Status、URI。该管理接口允许返回 Processing；用 `GET /v1/portrait/asset/<Id>` 查询，Active 后再生成。即使客户端提前提交生成，网关也会等待并校验 Active。

导入 legomind 人像包时，先将选中的 PNG 放入 TokenHub 可读取的文件存储，再调用入库接口。原包的 asset_id 只作为来源标识，必须另存“来源 asset_id → 当前账户新素材 Id / URI”的映射，不能假定源账户素材 ID 可跨账户使用。此 PR 不包含整包下载、批量导入脚本或素材选择 UI。

## 管理接口

提供 asset-group 创建/列表/详情/改名/删除，asset 上传/列表/详情/删除。手动创建组使用 `POST /v1/portrait/asset-group`，请求体 `{"Name":"虚拟主播","GroupType":"AIGC"}`；名称最多 64 个字符。其他 GroupType 拒绝。列表仅返回本调用者本账户绑定的 AIGC 记录；素材列表需在 Filter.GroupIds 中指定一个组。列表为本地快照，单个 GET 查询实时上游状态。

默认组及自动入库素材使用确定性 Name，以恢复未知创建结果；素材 Name 不超过 64 个字符。AIGC 组真实响应没有 Status，按 Id、GroupType 和返回的 ProjectName 校验；若显式返回 Status，则仍检查 active。素材必须达到 Active。自动入库素材暂不开放改名。删除是显式管理操作，不在失败、超时、重试或回滚时自动执行；上游删除成功后保留本地失效记录。已删除默认组不会自动重建，需要明确选择新的 AIGC 组。

TokenHub 实际 GET 接口要求 JSON 请求体包含 Id 和 ProjectName。网关替调用方补齐，下游 GET 无需请求体。DELETE 采用相同的显式 Id / ProjectName JSON；真实删除尚未联调。

## 复用、超时与收费边界

- 下载素材仅为流式计算 SHA-256，不保存源文件或签名 URL；单文件最多 100 MiB，下载最多 30 秒，限制公网 HTTP/HTTPS，连接时校验实际 IP，重定向同样校验。
- 缓存范围包含用户/令牌、渠道、上游凭据指纹、项目、资产组、素材类型和内容摘要。更换签名但内容相同可以复用，内容改变必须重新入库。密钥更换需重新绑定，不能跨账户复用。
- 数据库唯一预留记录限制并发重复创建默认组和素材。创建结果不明或进程中断后，按确定性 Name 查询恢复；未查到则暂停重复创建并要求核对上游记录。
- 素材准备总时限 55 秒。Processing 超时保留已获得的素材 ID，后续请求继续查询；Failed、已删除、归属不匹配均阻止生成。
- 前置失败返回 HTTP 400 和具体 portrait_* code，兼容现有视频客户端的“明确未受理”分类；素材管理 API 自身使用 400/404/409/410/422/429 等状态。
- 前置失败不进入 new-api 视频预扣费。素材库自身是否收费遵循上游条款，不能由视频未扣费推断素材服务免费。
- 启用渠道不自动跨渠道重试，包括生成请求结果不明的情况，避免资产账户错配或重复生成。
- 创建组 10 QPS，列表各 10 QPS，组 GET 10 QPS，素材 GET 100 QPS，更新合计 10 QPS，删除合计 5 QPS。Redis 模式跨副本共享限流，未启用 Redis 时仅进程内共享；创建权益由上游控制。

## TokenHub 视频状态兼容

- 初期 unknown 当作进行中。
- 完成结果读取 metadata.url，缺少链接继续查询。
- 下载结果使用成片 URL，不把 TokenHub Bearer key 带到外部素材域名。
- 已知真人隐私拦截返回 portrait_input_privacy_rejected，提示使用可入库的虚拟人像，不引导活体认证。

## 上线顺序与当前验证

1. 代码审核后备份渠道配置和数据库，发布 new-api；保持业务流量原路由。
2. 启用独立 TokenHub 渠道，用调用方 token 上传一份虚拟人像，确认新素材达到 Active，并实际验证 Asset URI 出片。
3. 验收后将 OpenBox Seedance provider 从直连 TokenHub 改为自有 new-api，并确保上传和生成使用同一渠道身份。OpenBox 当前直连路线不会自动获得这项前置入库功能。
4. 回滚可恢复 OpenBox 原路由；已有任务完成前保留 TokenHub 渠道及兼容设置，保留素材记录，不自动删除上游资源。

已完成：本地自动 AIGC 组、上传后 Active 门控、超时复用、HTTP 仿真、并发/竞态、调用者隔离、原参数保留及生成前计费边界验证；TokenHub GET 的 JSON / Id 要求已做核验。

2026-10-16 真实 TokenHub 联调（直接使用现有 OpenBox 上游凭据，不是已部署的 new-api）：

- 从 legomind 包提取一张虚拟人像，上传文件存储后创建 AIGC 组 `group-20261016182611-qwn58`。
- 新素材 `asset-20261016182611-zhsq6` 已由 Processing 转为 Active，随后 GET 再次确认仍 Active，ProjectName 为 default。
- 使用原 `doubao-seedance-2-0-260128` 模型、4 秒、720p、9:16，以该 asset URI 提交一次生成，返回 HTTP 400 / InvalidParameter，明确称素材不存在，没有返回任务 ID。
- 说明入库成功，但推理端不能读取素材。这次诊断入库使用的 default 与文档业务项目不一致，不能据此认定 TokenHub 上游账户路由故障；需先确认正确业务项目和凭据绑定，再重新入库验证，不能报告端到端出片成功。没有自动重试，也没有改用其他模型或原始图片 URL。
- 联调同时修正了组缺少 Status、素材 Name 长度和恢复查询缺少 Filter.GroupType 的兼容问题，并识别实际的嵌套“素材不存在”生成错误。

TokenHub 反馈项目名有误后，于 2026-10-16 20:19:35（上海时间）重新以 `ProjectName: 深圳赞卫数字科技有限公司` 调用 CreateAssetGroup，对外接口仍返回 HTTP 400 / InvalidParameter.ProjectName；RequestId 为 `20261016201935EC161EAA731FC30F1D2E`。没有换项目上传或重新提交视频。需供应商确认该凭据实际绑定的准确 ProjectName，或修复文档指定项目的配置。复核证据见 [项目复核记录](evidence/tokenhub-project-recheck-20261016.json)。

脱敏证据见 [此前 default 项目诊断记录](evidence/tokenhub-virtual-portrait-live-20261016.json)。尚未完成：成功出片、真实 DELETE、生产部署、OpenBox 路由切换及整包导入。已发起一次被明确拒绝的生成请求；供应商账单未核对。

## 测试

```sh
go test -race ./service/tokenhubportrait ./relay/channel/task/sora ./controller ./router ./relay \
  -run 'Portrait|TokenHub|PublicTaskIDs|SourceURL|GenerationError' -count=1
go test ./model ./service ./relay/channel/task/... -count=1
go vet ./service/tokenhubportrait ./relay/channel/task/sora ./controller ./router ./relay
```

全仓回归存在基线限制：干净 main 同样有模型列表测试和 Claude 文件内容转换测试失败。根包需要先构建 web/classic/dist 与 web/default/dist 才能满足 go:embed，不能报告全仓测试通过。
