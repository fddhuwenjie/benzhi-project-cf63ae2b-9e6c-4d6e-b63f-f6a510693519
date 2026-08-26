# 洪水评级曲线资格认证服务

这是面向水文测站技术负责人、评级曲线分析员和独立质量复核员的本地 HTTP JSON 服务。服务以受控状态机管理洪水期水位流量评级曲线，从候选建档和测量基线冻结开始，依次完成证据与仪器资格核验、样本裁定、拟合及残差诊断、外推边界评估、偏差闭环、独立复核、限期试用、正式启用和不可变封存。

服务不提供浏览器页面或业务 CLI，所有业务操作均通过 `/api/v1` JSON API 完成。SQLite 保存案件快照、乐观并发 revision、`request_id` 幂等结果、测站有效版本和只追加 SHA-256 审计链。

## 构建与测试

```sh
go build ./cmd/server
go test ./...
```

完整的真实 HTTP 自检会使用临时 SQLite 数据库，走完成功流程并主动退出：

```sh
go run ./cmd/server -self-check -addr=127.0.0.1:19081
```

## 运行

默认仅监听高位回环地址 `127.0.0.1:19081`，数据库默认为当前目录的 `curve-certification.db`：

```sh
go run ./cmd/server
go run ./cmd/server -addr=127.0.0.1:19120 -db=./data/certification.db
PORT=19120 go run ./cmd/server
```

`-addr` 必须是明确的回环 IP 且端口不低于 1024。若同时提供 `-addr` 与 `PORT`，显式 `-addr` 优先。服务拒绝 `0.0.0.0`、非回环地址和非法端口。

## API 流程

所有写请求使用 `application/json`，并包含 `request_id`、`actor_id` 和 `expected_revision`。首次建档的 `expected_revision` 为 `0`；后续命令使用上一响应中的 `revision`。同一 `request_id` 会返回原事务响应并设置 `Idempotent-Replayed: true`，陈旧 revision 返回 HTTP `409`。

主要调用顺序如下：

1. `POST /api/v1/curve-cases` 创建候选案件。
   同一测站同一候选版本只允许一个未归档案件；`GET /api/v1/curve-cases` 可按测站、责任人、状态及归档标志组合筛选并使用稳定游标分页。
2. 向 `/evidence` 和 `/instrument-qualifications` 登记证据与校准追溯信息；`POST /evidence/batches` 可在单事务内登记 1 至 200 条证据并返回批次摘要。评级测次需分别绑定实际使用的流速仪和水位计，断面资料需绑定测量设备，绑定包含 `instrument_id`、`qualification_id` 和当前证书摘要。冻结前可通过 `/evidence/{evidence_id}/corrections` 更正并从 `/versions` 查询版本链；`/evidence/quality-preflight`、`/quality-rejudgments` 和 `/instrument-qualifications/coverage-matrix` 分别用于质量预检、原子批量复裁和逐绑定覆盖检查。
   仪器资格也支持 `/instrument-qualifications/{qualification_id}/corrections` 替代更正和 `/versions` 不可变版本查询；历史洪痕需登记洪水事件、基准面、垂向不确定度和可信等级。
	评估后如收到证书撤销、作废或追溯中断通知，调用 `/instrument-qualifications/{qualification_id}/invalidations` 登记原证书摘要、失效时刻和通知证据。服务只核查冻结清单中的实际绑定，自动建立重大偏差、失效既有复核/试用并释放试用占用；整改仍沿既有偏差闭环端点完成。
3. 先调用 `GET /baseline-preflight` 获取确定性清单和 `proposed_baseline_digest`，再随 `/freeze-baseline` 命令提交该摘要；摘要或 revision 变化会返回最新预检。`/qualify-evidence` 始终读取不可变清单所指版本。
   `/assessments` 接收双端请求边界与低、高端最大延伸比例；`GET /assessments/diagnostics?side=low` 可独立查询边界来源，`GET /assessments/{run_id}/replay-verification` 可无副作用复算并定位首个差异。
4. 通过 `/deviations` 建立偏差，并依次调用偏差下的 `/containment`、`/root-cause`、`/correction` 和 `/verification`；每次整改形成递增轮次，复验失败会回到 `correction_required`，新轮次必须使用变化后的证据。偏差清单支持 `state` 与 `severity` 筛选，没有偏差时调用 `/close-deviations`。
   `/deviations/action-queue?as_of=...` 提供时点行动队列，逾期整改需先由独立批准者通过 `/due-date-revisions` 追加改期记录。
5. 通过 `/reviews` 签署独立复核。退回决定使用结构化 `issues`，随后通过 `/reviews/issues/{issue_id}/responses` 逐项响应并调用 `/reviews/resubmit` 生成新轮次；`/reviews/history` 保留完整历史。最新轮次通过后由 `/trial-decisions` 签发唯一有效试用版本。
   签署前必须查询 `/reviews/preflight?reviewer_id=...`，并把返回的 `materials_digest` 原样提交到签署命令。
6. 向 `/trial-observations` 登记独立校核测次。录入错误或污染测次通过 `/trial-observations/{observation_id}/replacements` 原子作废替代，原记录以 `superseded` 保留并立即重算资格。超限会自动进入 `trial_suspended`，替代不会绕过 `/trial-suspensions/investigation` 和 `/recovery` 的调查与独立恢复判定。
   `/trial-progress` 返回低、中、高水位带覆盖、独立提交人数、持续时长、最大偏差和未满足门槛；污染区间测次不计入进度。
	试用截止后调用 `/trial-expiry-settlements`，由服务端时钟固化最终门槛快照。合格试用进入 `trial_qualified` 并保留测站占用；不合格或仍暂停的试用释放占用并退回复核入口。
7. 正式启用前调用 `GET /activation-preflight?effective_from=...`，并把响应中的 `current_version_digest` 原样提交给 `/activation-decisions`。测站 `/curve-history` 和 `/curve-as-of?as_of=...` 提供版本履历及按时点查询，未来决定不会提前成为 `/current-curve`。
8. 调用 `/archive` 封存，随后通过同名 GET 路由和 `/timeline` 验证档案及审计链。时间线支持 `event_type`、`actor_id`、`request_id`、序号范围、`cursor` 和 `limit` 组合查询，每页返回前向摘要、链头和完整性证明。
   `/archive/verification` 支持携带保存的 `archive_digest` 验证整档，或用 `kind`、`id` 查询单项清单位置和摘要证明。
	`GET /api/v1/stations/{station_id}/certification-continuity` 从测站维度核查历次替代链、生效区间、正式启用决定、档案摘要/成员证明及当前指针，单项损坏不会阻止其他版本出具核验结果。

可用 `GET /healthz` 查看服务健康状态，或用 `GET /api/v1/stations/{station_id}/current-curve` 查询测站当前正式曲线。归档案件拒绝任何后续业务写入。
