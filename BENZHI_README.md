# BENZHI_README

## 项目说明
- 项目：benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519
- 项目用途：已实现洪水评级曲线资格认证本地 HTTP JSON 服务，覆盖基线冻结、证据与仪器资格、样本质量、拟合残差和外推诊断、偏差复验闭环、独立复核、限期试用、正式启用、SQLite 持久化以及可验证归档全流程。
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 项目描述
- 项目名称：洪水评级曲线资格认证服务
- 项目介绍：面向水文技术负责人和独立复核员的本地 HTTP JSON 服务，用一条受状态机约束的流程完成洪水期流量评级曲线从候选建档、测量证据核验、模型诊断、偏差闭环、限期试用到正式启用及可验证封存，避免未经论证的水位流量换算关系进入业务使用。
- 项目概述：面向水文技术负责人和独立复核员的本地 HTTP JSON 服务，用一条受状态机约束的流程完成洪水期流量评级曲线从候选建档、测量证据核验、模型诊断、偏差闭环、限期试用到正式启用及可验证封存，避免未经论证的水位流量换算关系进入业务使用。
- 核心工作流：候选评级曲线案件从草稿建档开始，冻结测次与仪器基线后依次完成证据追溯、样本质量裁定、拟合与残差诊断、外推边界评估；不合格项必须进入偏差整改和复验，随后由职责隔离的复核员签发限期试用，在试用观测达到判定门槛后作出正式启用决定，并将版本、决定和全量审计证据封存为不可再变更的认证档案。
- 对外接口：仅提供版本化 HTTP JSON API；调用方通过案件资源、证据资源、评估命令、偏差命令、复核命令和归档查询推进完整流程，不提供浏览器页面、CLI 或桌面界面。服务支持 -addr=127.0.0.1:<port>，也支持以 PORT 端口号绑定 127.0.0.1:<PORT>，默认监听 127.0.0.1:19081，拒绝默认绑定 0.0.0.0 或常见低位端口。

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...

cd /app && GOTOOLCHAIN=local go run ./cmd/server -self-check -addr=127.0.0.1:19081

cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh

./build_benzhi_docker.sh benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519-amd64 linux/amd64

./build_benzhi_docker.sh benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519-arm64 linux/arm64

docker run -it benzhi-project-cf63ae2b-9e6c-4d6e-b63f-f6a510693519-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/server -self-check -addr=127.0.0.1:19081`
