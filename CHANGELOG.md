# FlowState Changelog

All notable changes to the FlowState project will be documented in this file.

## 1.0.0 (2026-03-25)

### Features

* **build:** add ai-commit script for proper AI attribution ([5559c18](https://github.com/baphled/FlowState/commit/5559c1836b7ad043f7822fa07cdace9b6155d0f1))
* **build:** add ai-commit skill for attribution workflow ([583dbe3](https://github.com/baphled/FlowState/commit/583dbe32d14de56dba7cf154827a26fa97f4c8ce))
* initial project setup ([6fb6424](https://github.com/baphled/FlowState/commit/6fb642475e5eab5977bfb56e508da1bb1125405a))
* **oauth:** add GitHub OAuth Device Flow with encrypted token storage ([7f457c3](https://github.com/baphled/FlowState/commit/7f457c31049b43b4cd6bcad36f735361ab6cb5ef))
* **provider:** add config and provider infrastructure with OAuth support ([19aab70](https://github.com/baphled/FlowState/commit/19aab70011cd89e801d8950d96aeafc8e4fb184a))

### Bug Fixes

* **ci:** correct gofmt formatting and godog feature paths ([bc6b0eb](https://github.com/baphled/FlowState/commit/bc6b0eb5816bca2af420a29ede98de3667f043d8))
* **ci:** install golangci-lint v2 in release workflow ([8914f88](https://github.com/baphled/FlowState/commit/8914f88eaf8e30706154c1de39cb976b162f7ffe))
* **ci:** pin golangci-lint to v1.59.1 for config v2 support ([1ae7c5d](https://github.com/baphled/FlowState/commit/1ae7c5ddee26ec86a72169ff48acbc78b24c0706))
* **ci:** pin golangci-lint to v2.11.3 (action requires full semver) ([0d61b80](https://github.com/baphled/FlowState/commit/0d61b800f004947122e5b0c5fcb10464fd0286df))
* **ci:** remove golangci-lint, use go vet + staticcheck only ([c55759a](https://github.com/baphled/FlowState/commit/c55759a81141789f74f63ba453010aaba3110420))
* **ci:** update golangci-lint to v1.61.0 ([c71179e](https://github.com/baphled/FlowState/commit/c71179e820e2f2483085831bea6b9bf3f5e8d9e6))
* **ci:** use golangci-lint v1.54.2 (stable, Go 1.25 compatible) ([9a707fc](https://github.com/baphled/FlowState/commit/9a707fce4b8db5b65c5557ba597f7e3d5c6634ea))
* **ci:** use golangci-lint-action@v8 with version v2 ([bd21d93](https://github.com/baphled/FlowState/commit/bd21d93bc025161fdbdc2dc7821cb30a904182dd))
* **lint:** make deadcode fail on actual violations ([536778e](https://github.com/baphled/FlowState/commit/536778e4d9e17c9017b53f0554b93260dba3ff04))
