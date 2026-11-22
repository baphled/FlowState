# FlowState Changelog

All notable changes to the FlowState project will be documented in this file.

## [1.9.0](https://github.com/baphled/FlowState/compare/v1.8.1...v1.9.0) (2026-11-22)

### Features

* **config:** take provider settings from the environment only ([#385](https://github.com/baphled/FlowState/issues/385)) ([ef8084c](https://github.com/baphled/FlowState/commit/ef8084cc0227393a796ef6f5415092c3b16f5bac))

### Bug Fixes

* **engine:** cancel the unfinished children of a cancelled swarm run ([#402](https://github.com/baphled/FlowState/issues/402)) ([dae9321](https://github.com/baphled/FlowState/commit/dae9321201d0543d1eb1bec0fc2c0923b29f6ccc))
* **engine:** stop a cancelled gate gracefully before killing it ([#396](https://github.com/baphled/FlowState/issues/396)) ([5890451](https://github.com/baphled/FlowState/commit/58904515c8c58e4a6f3158520f9056e98919d304))
* **server:** record why the run route cancelled a run ([#390](https://github.com/baphled/FlowState/issues/390)) ([03d5916](https://github.com/baphled/FlowState/commit/03d5916459dcf7cbfee7726f8ea92b349da6bca9))
* **server:** say that a session is cancelled when its settle failed ([#397](https://github.com/baphled/FlowState/issues/397)) ([e2fac89](https://github.com/baphled/FlowState/commit/e2fac89c2a3c3b6f6e7a2568f4d99b2a32716938))
* **session:** record which signal cancelled a run ([#384](https://github.com/baphled/FlowState/issues/384)) ([e561ade](https://github.com/baphled/FlowState/commit/e561ade83a10feb2d21d83d463890421a6400777))
* **store:** give an event append a definite outcome under cancellation ([#393](https://github.com/baphled/FlowState/issues/393)) ([359c609](https://github.com/baphled/FlowState/commit/359c609ca2e24960effce1ce21624ee6c22402ed))
* **store:** keep the writer in step when a steering drain rolls back ([#394](https://github.com/baphled/FlowState/issues/394)) ([514ccbd](https://github.com/baphled/FlowState/commit/514ccbd20d753d7a0b63fac593f95f3a48e5a387))
* **store:** record the child sessions a swarm run created ([#395](https://github.com/baphled/FlowState/issues/395)) ([d35e005](https://github.com/baphled/FlowState/commit/d35e005e177060fc73cc82c6a5f2dfe3138fd752))
* **workflow:** cap admission statuses and give a missing check a deadline ([#387](https://github.com/baphled/FlowState/issues/387)) ([24969bd](https://github.com/baphled/FlowState/commit/24969bd5ba0722ef6bc339dc95cabfb678eeee13))
* **workflow:** fail a misspelled review line instead of waiting for one ([#399](https://github.com/baphled/FlowState/issues/399)) ([f2103b5](https://github.com/baphled/FlowState/commit/f2103b5d6d91c300f4581edd14eff146962c5dba))
* **workflow:** reject non-regular files in the admission publisher ([#386](https://github.com/baphled/FlowState/issues/386)) ([b947973](https://github.com/baphled/FlowState/commit/b947973a49b464d9db1ae8acde2fbc20c9e27992))

### Code Refactoring

* **store:** index receipts through the orm store ([#398](https://github.com/baphled/FlowState/issues/398)) ([fc79d88](https://github.com/baphled/FlowState/commit/fc79d880aa59b8a610ab18a2c400f78167c1a703))
* **store:** remove the unused legacy event writer ([#392](https://github.com/baphled/FlowState/issues/392)) ([37fea78](https://github.com/baphled/FlowState/commit/37fea78cc3eafe12d81039f9a83879aae021beea))

## [1.8.1](https://github.com/baphled/FlowState/compare/v1.8.0...v1.8.1) (2026-11-22)

### Bug Fixes

* **app:** class a rejected swarm run as receipt_invalid ([#379](https://github.com/baphled/FlowState/issues/379)) ([d506e85](https://github.com/baphled/FlowState/commit/d506e85865227a5f958931d30ec54f5407ebd61b))
* **app:** let a cancelled caller stop a swarm run ([#373](https://github.com/baphled/FlowState/issues/373)) ([7d8c959](https://github.com/baphled/FlowState/commit/7d8c959c684100f2da0560da60616327909ce74b))
* **engine:** stop a cancelled gate's whole process group ([#380](https://github.com/baphled/FlowState/issues/380)) ([e24aa1e](https://github.com/baphled/FlowState/commit/e24aa1e515204266551ee34f48cbdd5a2628e598))
* **provider:** keep a length finish reason once the stream reports it ([#383](https://github.com/baphled/FlowState/issues/383)) ([d81d1ed](https://github.com/baphled/FlowState/commit/d81d1ed616aaff22190174dd3dcaedc880d7ee09))
* **provider:** send an explicit z.ai token limit as given ([#375](https://github.com/baphled/FlowState/issues/375)) ([61e8234](https://github.com/baphled/FlowState/commit/61e82345e0a54456524f70136897c1ace3f92eda))
* **session:** cancel a run when the terminal hangs up ([#382](https://github.com/baphled/FlowState/issues/382)) ([87f5a50](https://github.com/baphled/FlowState/commit/87f5a505c61282627ac11d14f6b0d6469d0fdc27))
* **store:** order sessions, messages and swarm runs chronologically ([#381](https://github.com/baphled/FlowState/issues/381)) ([4c1185f](https://github.com/baphled/FlowState/commit/4c1185f22b88e8091fc3df74e1502a783ed4458a))
* **workflow:** let admission accept attested file deletions ([#374](https://github.com/baphled/FlowState/issues/374)) ([1bba090](https://github.com/baphled/FlowState/commit/1bba090805e3aafa06d79fbc57888eac63b3e42e))

### Code Refactoring

* **engine:** remove the unused legacy runtime package ([#377](https://github.com/baphled/FlowState/issues/377)) ([82f774d](https://github.com/baphled/FlowState/commit/82f774dfff2eb032a0a34d462b93a8a273af6ee9))

## [1.8.0](https://github.com/baphled/FlowState/compare/v1.7.1...v1.8.0) (2026-11-21)

### Features

* **session:** report a truncated final turn in session doctor ([#362](https://github.com/baphled/FlowState/issues/362)) ([6fcf6b6](https://github.com/baphled/FlowState/commit/6fcf6b6241c5e51460e16218183f877d6819d93c))

### Bug Fixes

* **build:** harden the local preflight and pin ci against drift ([#371](https://github.com/baphled/FlowState/issues/371)) ([ad0ce86](https://github.com/baphled/FlowState/commit/ad0ce86fdde7fdd51f8e1a5683a14729e2445ce7))
* **provider:** keep the anthropic subscription child on the subscription ([#365](https://github.com/baphled/FlowState/issues/365)) ([acd422d](https://github.com/baphled/FlowState/commit/acd422d30a89ba405d0ec7273786e9a4c440e713))
* **provider:** report the finish reason on every z.ai route ([#370](https://github.com/baphled/FlowState/issues/370)) ([027d628](https://github.com/baphled/FlowState/commit/027d628ecd5a236d707115c03870cc85a20409c4))
* **server:** answer cancelled reads correctly and refuse an exposed bind ([#367](https://github.com/baphled/FlowState/issues/367)) ([e0b9424](https://github.com/baphled/FlowState/commit/e0b9424b432c33665628698b33cb5a8e7873decb))
* **server:** decode the create body once and make stream specs fail fast ([#368](https://github.com/baphled/FlowState/issues/368)) ([8ff70ba](https://github.com/baphled/FlowState/commit/8ff70ba459bec256111765b499c7ec4a26fd2825))
* **session:** report only provider failures as provider errors ([#361](https://github.com/baphled/FlowState/issues/361)) ([3e6c55b](https://github.com/baphled/FlowState/commit/3e6c55bfda2e34b11141b9872457ecdb2fe7df4e))
* **session:** surface truncation and classify internal failures clearly ([#366](https://github.com/baphled/FlowState/issues/366)) ([a707c0e](https://github.com/baphled/FlowState/commit/a707c0ecc3dacaa0a026a6f8ea697c0afcf1ee23))
* **workflow:** publish a waiting admission as pending, not failure ([#369](https://github.com/baphled/FlowState/issues/369)) ([54ebd6d](https://github.com/baphled/FlowState/commit/54ebd6d9e99138f46af33cefce51c95e5a186884))
* **workflow:** stop re-publishing admission statuses for unadmitted heads ([#364](https://github.com/baphled/FlowState/issues/364)) ([032e772](https://github.com/baphled/FlowState/commit/032e772198b7969aefb011861475b7147948ef97))

## [1.7.1](https://github.com/baphled/FlowState/compare/v1.7.0...v1.7.1) (2026-11-21)

### Bug Fixes

* **app:** fail a turn on persistence errors and record its finish reason ([#358](https://github.com/baphled/FlowState/issues/358)) ([41903a0](https://github.com/baphled/FlowState/commit/41903a08b5fc1a40d5577c5c1a35ba9b58a7784e))
* **provider:** report neutral finish reasons on the anthropic subscription route ([#357](https://github.com/baphled/FlowState/issues/357)) ([36f80bb](https://github.com/baphled/FlowState/commit/36f80bbaddb039ba6f14b33fa7b843f1186fabde))

## [1.7.0](https://github.com/baphled/FlowState/compare/v1.6.1...v1.7.0) (2026-11-21)

### Features

* **config:** give the cli its own providers.yaml ([#356](https://github.com/baphled/FlowState/issues/356)) ([6ec9f49](https://github.com/baphled/FlowState/commit/6ec9f49804c8ba56a9301aec0a65942a2e0b60de))

## [1.6.1](https://github.com/baphled/FlowState/compare/v1.6.0...v1.6.1) (2026-11-21)

### Bug Fixes

* **provider:** send an empty system prompt on the anthropic subscription route ([#355](https://github.com/baphled/FlowState/issues/355)) ([30941c8](https://github.com/baphled/FlowState/commit/30941c8075673a185e605f1f553dab8e1ad54cd8))

## [1.6.0](https://github.com/baphled/FlowState/compare/v1.5.0...v1.6.0) (2026-11-21)

### Features

* **build:** add a local preflight mirroring the blocking ci jobs ([#348](https://github.com/baphled/FlowState/issues/348)) ([511932d](https://github.com/baphled/FlowState/commit/511932d44fcc01b6eddd3ea0fcd2d6050ebcb099))
* **provider:** add anthropic subscription provider ([#345](https://github.com/baphled/FlowState/issues/345)) ([c1d6f49](https://github.com/baphled/FlowState/commit/c1d6f49edc9d3797432e04d8ec9cb94189a4e6b5))
* **provider:** add api key authentication to the anthropic provider ([#354](https://github.com/baphled/FlowState/issues/354)) ([8cd2207](https://github.com/baphled/FlowState/commit/8cd2207810b00cb4fd93a5644e518c9969938c8f))
* **provider:** run anthropic through the claude code binary ([#347](https://github.com/baphled/FlowState/issues/347)) ([6627bcb](https://github.com/baphled/FlowState/commit/6627bcb7938c439655630be99b1c3f934d318a09))
* **server:** add budget and steering endpoints ([#338](https://github.com/baphled/FlowState/issues/338)) ([4fbcbd9](https://github.com/baphled/FlowState/commit/4fbcbd941aa0a5eb3603e4af5aff05d30ef155f5))
* **server:** add events stream run and session creation endpoints ([#340](https://github.com/baphled/FlowState/issues/340)) ([131b1d3](https://github.com/baphled/FlowState/commit/131b1d33e4d3588353f8647030f7ee979f4db9fd))
* **server:** add swarm and receipt endpoints ([#339](https://github.com/baphled/FlowState/issues/339)) ([4d86c3c](https://github.com/baphled/FlowState/commit/4d86c3c459975d129c9bcdb71b0cecb97ab24ed9))

### Bug Fixes

* **app:** append session.cancelled before the cancelled status ([#342](https://github.com/baphled/FlowState/issues/342)) ([226149e](https://github.com/baphled/FlowState/commit/226149ed13c58706cadbb476ddc12d6160a5af6c))
* **app:** record the provider error before marking a loop run failed ([#352](https://github.com/baphled/FlowState/issues/352)) ([8446e43](https://github.com/baphled/FlowState/commit/8446e4321deab31f99be0a63617500d4694fac00))
* **ci:** size pr labels against the kind-aware review budget ([#341](https://github.com/baphled/FlowState/issues/341)) ([c337adc](https://github.com/baphled/FlowState/commit/c337adce0a1e9087781bd2ccc78732c69b8d3252))
* **provider:** close review findings on the anthropic provider ([#350](https://github.com/baphled/FlowState/issues/350)) ([c0e4ec7](https://github.com/baphled/FlowState/commit/c0e4ec7b609a60a534a5e8c7406cd000d40eb728))
* **server:** bound and strictly decode request bodies ([#351](https://github.com/baphled/FlowState/issues/351)) ([8d4e57f](https://github.com/baphled/FlowState/commit/8d4e57f76c1ab39dc72a2ff1514ef64f04b2e4fc))
* **server:** cancel reads and finish writes on shutdown ([#343](https://github.com/baphled/FlowState/issues/343)) ([dca3721](https://github.com/baphled/FlowState/commit/dca37216635b09485250aa13a1e353342ad60322))
* **server:** validate the events cursor and pin stream behaviour ([#353](https://github.com/baphled/FlowState/issues/353)) ([46e7313](https://github.com/baphled/FlowState/commit/46e7313766aad4ca77cd2a3c4522553aa15c2928))
* **workflow:** keep the admission publisher alive through skew and restarts ([#346](https://github.com/baphled/FlowState/issues/346)) ([7795ae0](https://github.com/baphled/FlowState/commit/7795ae06db97144737b2a737a2eb387d099fe74e))
* **workflow:** make the merge admission job a publisher liveness monitor ([#349](https://github.com/baphled/FlowState/issues/349)) ([98f859b](https://github.com/baphled/FlowState/commit/98f859b38eb80fa7fabbf5328576a90c425859ae))

## [1.5.0](https://github.com/baphled/FlowState/compare/v1.4.0...v1.5.0) (2026-11-20)

### Features

* **build:** add a kind-aware changed-path budget ([#335](https://github.com/baphled/FlowState/issues/335)) ([3d5438a](https://github.com/baphled/FlowState/commit/3d5438a791c116630bcc25d9a9f22bd51ee822ac))
* **build:** confine net/http to transport adapters via internal/server ([#331](https://github.com/baphled/FlowState/issues/331)) ([fc73cf1](https://github.com/baphled/FlowState/commit/fc73cf10de80753a22e94ed4eaaa6c8fdd6a48fb))
* **server:** add serve skeleton with health and session list/get ([#336](https://github.com/baphled/FlowState/issues/336)) ([22a7942](https://github.com/baphled/FlowState/commit/22a7942c6d56bd27c1accd061169191124653f68))
* **server:** add session close cancel and doctor endpoints ([#337](https://github.com/baphled/FlowState/issues/337)) ([218b549](https://github.com/baphled/FlowState/commit/218b549b3d1134ae009692175c9fa4a0679ffa06))
* **session:** add read-only session doctor diagnosis ([#330](https://github.com/baphled/FlowState/issues/330)) ([aab65b9](https://github.com/baphled/FlowState/commit/aab65b9dd9b3e6086cd86822b32bb24ddc6a8245))

### Bug Fixes

* **app:** count content deltas as crash run-activity evidence ([#332](https://github.com/baphled/FlowState/issues/332)) ([038fda7](https://github.com/baphled/FlowState/commit/038fda73f51ae227f1241179bf0ee5d832fa5dbe))
* **build:** allow cmd to import the server adapter in contract lint ([#334](https://github.com/baphled/FlowState/issues/334)) ([4308edd](https://github.com/baphled/FlowState/commit/4308eddc4146967cd309b54f5a3f4e3a868dd5eb))

## [1.4.0](https://github.com/baphled/FlowState/compare/v1.3.0...v1.4.0) (2026-11-19)

### Features

* **app:** budget overview wrapper on the owning layer ([#327](https://github.com/baphled/FlowState/issues/327)) ([1496d56](https://github.com/baphled/FlowState/commit/1496d56bcefff642932ace414ec3699e5d58d882))
* **session:** schema keys for message tool and swarm outputs ([#326](https://github.com/baphled/FlowState/issues/326)) ([faf1560](https://github.com/baphled/FlowState/commit/faf1560dde492fa50b418c95437b516b54256b27))

### Bug Fixes

* **workflow:** distinguish budget gate failure classes on push ([#328](https://github.com/baphled/FlowState/issues/328)) ([4661767](https://github.com/baphled/FlowState/commit/466176714e861eb383def5402efd312648306a5b))
* **workflow:** skip pr budget gate on release aggregate pushes ([#325](https://github.com/baphled/FlowState/issues/325)) ([b97401f](https://github.com/baphled/FlowState/commit/b97401f3e8ee85496d9a0b6bfe70a8915ed45cbe))

## [1.3.0](https://github.com/baphled/FlowState/compare/v1.2.1...v1.3.0) (2026-11-19)

### Features

* **app:** child runs draw from budget reservations ([#313](https://github.com/baphled/FlowState/issues/313)) ([65cb11b](https://github.com/baphled/FlowState/commit/65cb11bb0e994c18aca611688563eabffe2c2076))
* **app:** child-session creation wrapper ([#310](https://github.com/baphled/FlowState/issues/310)) ([11fd887](https://github.com/baphled/FlowState/commit/11fd8870a8889687798a4e33a623a83b197cc972))
* **app:** delegation chain queries with depth enforcement ([#317](https://github.com/baphled/FlowState/issues/317)) ([f4539af](https://github.com/baphled/FlowState/commit/f4539afb9d42edf864f44959c4d0b4e447362d4a))
* **app:** durable steering message ledger with run-preamble drain ([#318](https://github.com/baphled/FlowState/issues/318)) ([baf1cae](https://github.com/baphled/FlowState/commit/baf1cae7e204d60e04cc44ea458d2809eac796f2))
* **app:** swarm execution wiring for delegated children ([#322](https://github.com/baphled/FlowState/issues/322)) ([a609f4e](https://github.com/baphled/FlowState/commit/a609f4e425baf8ba298eb53b20a74271fe9e9375))
* **app:** swarm validate and run commands ([#323](https://github.com/baphled/FlowState/issues/323)) ([86d7d53](https://github.com/baphled/FlowState/commit/86d7d5379513d41021fea18300fd6824bbca8e4a))
* **session:** budget reservations and overview on the session surface ([#315](https://github.com/baphled/FlowState/issues/315)) ([3d0bc0b](https://github.com/baphled/FlowState/commit/3d0bc0ba03db8842e94907983438dfce48ba06f3))
* **session:** child sessions via session new --parent ([#311](https://github.com/baphled/FlowState/issues/311)) ([ccb8372](https://github.com/baphled/FlowState/commit/ccb83724b6a16d6250fbc7825a6dbc950052f790))
* **session:** events --follow tails child streams ([#321](https://github.com/baphled/FlowState/issues/321)) ([4b2eebf](https://github.com/baphled/FlowState/commit/4b2eebffa15f3fe5aec3b3dfb6aa61a039243840))
* **session:** steering directives via session message ([#319](https://github.com/baphled/FlowState/issues/319)) ([1edcffd](https://github.com/baphled/FlowState/commit/1edcffdba9f8d89628c60d8f564ee17303f61bd1))
* **store:** durable budget reservations ledger ([#312](https://github.com/baphled/FlowState/issues/312)) ([dba96b9](https://github.com/baphled/FlowState/commit/dba96b912e78a925c4378817ea8daefe790631e7))
* **store:** durable swarm-run record ([#320](https://github.com/baphled/FlowState/issues/320)) ([036836b](https://github.com/baphled/FlowState/commit/036836b853ff05fb405c06973ad9cffeae664599))
* **store:** gorm-managed schema for the session store ([#308](https://github.com/baphled/FlowState/issues/308)) ([7f7fee5](https://github.com/baphled/FlowState/commit/7f7fee5f57c43cc4a3a00f08d00e16a7dc4f879a))
* **store:** orm session writer with child-session linkage ([#309](https://github.com/baphled/FlowState/issues/309)) ([c6670f6](https://github.com/baphled/FlowState/commit/c6670f64296a096d062098f28b84ea1fb49ac308))

### Bug Fixes

* **app:** resolve run budgets on every run path ([#314](https://github.com/baphled/FlowState/issues/314)) ([eb0da94](https://github.com/baphled/FlowState/commit/eb0da94fbd898d973855b1a246139d04732c2561))
* **ci:** close unbalanced fi in ci.yml budget fallback branch ([#307](https://github.com/baphled/FlowState/issues/307)) ([9f4a044](https://github.com/baphled/FlowState/commit/9f4a044092690916b57355ae87d0ec6493281a6b))

## [1.2.1](https://github.com/baphled/FlowState/compare/v1.2.0...v1.2.1) (2026-11-18)

### Bug Fixes

* **app:** accept m1-dogfood in receipt capability enum ([#306](https://github.com/baphled/FlowState/issues/306)) ([c41e25b](https://github.com/baphled/FlowState/commit/c41e25ba9b2d0521269e0cacef318b8ca2468d20))
* **ci:** pre-commit budget hook derives kind from branch prefix ([#305](https://github.com/baphled/FlowState/issues/305)) ([0146b61](https://github.com/baphled/FlowState/commit/0146b61cc3768656300a6469bc76eb06894a8b60))

## [1.2.0](https://github.com/baphled/FlowState/compare/v1.1.0...v1.2.0) (2026-11-18)

### Features

* **app:** crash-recovery detection and recovery for session run ([#296](https://github.com/baphled/FlowState/issues/296)) ([d9d5dee](https://github.com/baphled/FlowState/commit/d9d5dee35c3fcff410d427b853d0f08b0282a898))
* **app:** schema-validate --json output against schemas ([#295](https://github.com/baphled/FlowState/issues/295)) ([0822225](https://github.com/baphled/FlowState/commit/08222256e3a4b47d7edfa8504c3d916aacee9e77))

## [1.1.0](https://github.com/baphled/FlowState/compare/v1.0.0...v1.1.0) (2026-11-18)

### Features

* **app:** add cancellation cascade over child links ([#166](https://github.com/baphled/FlowState/issues/166)) ([8a8aad4](https://github.com/baphled/FlowState/commit/8a8aad4bd90dcbf89b33412873ec7761041d516b))
* **app:** add child-session linkage to admission control ([#163](https://github.com/baphled/FlowState/issues/163)) ([55c89ae](https://github.com/baphled/FlowState/commit/55c89ae9ec4b7c54fbacbe0fa5ed5e74575e7175))
* **app:** add configured swarm manifest with completion gates ([#168](https://github.com/baphled/FlowState/issues/168)) ([ccb248b](https://github.com/baphled/FlowState/commit/ccb248b7417cc11b438e968211804b081babd738))
* **app:** add dogfood run with double live gate and receipt ([#149](https://github.com/baphled/FlowState/issues/149)) ([4639f3a](https://github.com/baphled/FlowState/commit/4639f3ab2c2515d86c60f7425c5c7f7e4f860aa3))
* **app:** add fail-closed recovery decision from checkpoints ([#167](https://github.com/baphled/FlowState/issues/167)) ([b2d0a3c](https://github.com/baphled/FlowState/commit/b2d0a3cedd3cc24e06e5c5cb68e4208867ab2bf8))
* **app:** add hierarchical budget pool for swarm child turns ([#162](https://github.com/baphled/FlowState/issues/162)) ([3f3f95e](https://github.com/baphled/FlowState/commit/3f3f95e0ce947565ab9ee732f9e25f013b3500b0))
* **app:** add reservation ledger for hierarchical budget accounting ([#164](https://github.com/baphled/FlowState/issues/164)) ([7ed2b30](https://github.com/baphled/FlowState/commit/7ed2b30a921759fed825e305964c7cedc59f8000))
* **app:** add steering queue for mid-run child direction ([#165](https://github.com/baphled/FlowState/issues/165)) ([a4373a1](https://github.com/baphled/FlowState/commit/a4373a149206d883abe2bfd84550608008c7a720))
* **app:** add swarm admission control with scope-bound graph validation ([#160](https://github.com/baphled/FlowState/issues/160)) ([9d8a67f](https://github.com/baphled/FlowState/commit/9d8a67fb9cf4a016125daa92630de185dcb792c4))
* **app:** add the closed cli error-code set ([#138](https://github.com/baphled/FlowState/issues/138)) ([67b1ae2](https://github.com/baphled/FlowState/commit/67b1ae2b6e15fb945566f7dc48172c63260d85c5)), closes [#126](https://github.com/baphled/FlowState/issues/126)
* **app:** additive turn engine with budget support ([#115](https://github.com/baphled/FlowState/issues/115)) ([9ec5732](https://github.com/baphled/FlowState/commit/9ec5732a8d7c41a5168a254ea839daa2d2001b83)), closes [#103](https://github.com/baphled/FlowState/issues/103)
* **app:** deterministic session composition from flowstate.json ([#292](https://github.com/baphled/FlowState/issues/292)) ([ccd7a6f](https://github.com/baphled/FlowState/commit/ccd7a6f716a92a43487c961f8b211342c3a7cc6c))
* **app:** emit receipts in the v2 acceptance schema ([#159](https://github.com/baphled/FlowState/issues/159)) ([257f6d2](https://github.com/baphled/FlowState/commit/257f6d20d83cdc879ee5739f093911ee2d0cf12f)), closes [#144](https://github.com/baphled/FlowState/issues/144)
* **app:** expose registered session tool definitions ([#219](https://github.com/baphled/FlowState/issues/219)) ([8458f4d](https://github.com/baphled/FlowState/commit/8458f4d1f6dd92e067f9810d6b7b3d81aba24a8c))
* **app:** load swarm manifests and drive admission through gates ([#180](https://github.com/baphled/FlowState/issues/180)) ([a1a9074](https://github.com/baphled/FlowState/commit/a1a9074d3e6d54f8880a72320bebf258f25fd127))
* **app:** m2 provider-driven execution loop ([#260](https://github.com/baphled/FlowState/issues/260)) ([b644958](https://github.com/baphled/FlowState/commit/b644958eb2de68aa9b349683c864df1390a46e04))
* **app:** per-call correlated event sink adapter ([#259](https://github.com/baphled/FlowState/issues/259)) ([04c72b8](https://github.com/baphled/FlowState/commit/04c72b8fa731dce18e37ebf3f4d610684af2752f))
* **app:** rename dogfood seams to selftest domain terms ([#150](https://github.com/baphled/FlowState/issues/150)) ([e4cc427](https://github.com/baphled/FlowState/commit/e4cc4274a9ea9820cd10c307277c01175fd0c9bd))
* **app:** replay bounded correlated conversation history ([#220](https://github.com/baphled/FlowState/issues/220)) ([b090d44](https://github.com/baphled/FlowState/commit/b090d449bac209e9797293687491faa305556cdb))
* **app:** resume paused sessions explicitly on session run ([#282](https://github.com/baphled/FlowState/issues/282)) ([347411d](https://github.com/baphled/FlowState/commit/347411d0b89a0a045623163219f54a6dbee9e2ff))
* **app:** wire --budget and --max-tokens through session run ([#141](https://github.com/baphled/FlowState/issues/141)) ([52d3e89](https://github.com/baphled/FlowState/commit/52d3e890ae5342108d7cad49bb2f307401a869d1))
* **app:** wire the tool execution loop into session run ([#278](https://github.com/baphled/FlowState/issues/278)) ([16f97c9](https://github.com/baphled/FlowState/commit/16f97c959d66dc0f206a3971a1f1f89126b6a7aa))
* **build:** live working-tree pr size feedback with commit-time hooks ([#113](https://github.com/baphled/FlowState/issues/113)) ([a85b18d](https://github.com/baphled/FlowState/commit/a85b18d41596f856a967721e0014cd21bbfca125))
* **ci:** enforce typed review budgets in pre-push qualification ([#272](https://github.com/baphled/FlowState/issues/272)) ([2218bc8](https://github.com/baphled/FlowState/commit/2218bc892eee70ec8eb1b845b71949e88b2e0d63))
* **ci:** wire review-handoff scripts into ci-next workflow ([#267](https://github.com/baphled/FlowState/issues/267)) ([2b37fb2](https://github.com/baphled/FlowState/commit/2b37fb222c6ebee8237893eaa13713e04763c0d8))
* **config:** honour providers.zai.plan and host from config.yaml ([#174](https://github.com/baphled/FlowState/issues/174)) ([3d41d73](https://github.com/baphled/FlowState/commit/3d41d73e976d15cbbae08715122aaacffe8fd5bd))
* **engine:** explicit m1 composition root ([#41](https://github.com/baphled/FlowState/issues/41)) ([9c3d775](https://github.com/baphled/FlowState/commit/9c3d77567016f1546c604d75a7c6a4279f5143be))
* **engine:** t019 sqlite driver qualification (rework of [#27](https://github.com/baphled/FlowState/issues/27)) ([#48](https://github.com/baphled/FlowState/issues/48)) ([a85052e](https://github.com/baphled/FlowState/commit/a85052e773ddec76dca25c647b54afc91826ab04))
* **provider:** add structured tool call contracts ([#217](https://github.com/baphled/FlowState/issues/217)) ([fb23bcc](https://github.com/baphled/FlowState/commit/fb23bcc3699f1adaf5a77771b4ade62ca21a6b6e))
* **provider:** add z.ai adapter and provider registry (m1-provider) ([#49](https://github.com/baphled/FlowState/issues/49)) ([d975ede](https://github.com/baphled/FlowState/commit/d975ede19a21cbf7d1c4ec28b9029f29bd935d6d))
* **provider:** decode zai coding tool streams ([#218](https://github.com/baphled/FlowState/issues/218)) ([a6e8055](https://github.com/baphled/FlowState/commit/a6e8055bac1a740dda690c7ff8905c336eb22d01))
* **provider:** default coding route to openai coding endpoint ([#176](https://github.com/baphled/FlowState/issues/176)) ([fd60b72](https://github.com/baphled/FlowState/commit/fd60b726d416d4fcc4e74a0176bdfbe26478eb64))
* **provider:** pass max_tokens through to the z.ai request body ([#142](https://github.com/baphled/FlowState/issues/142)) ([c9346fc](https://github.com/baphled/FlowState/commit/c9346fc569b256141669b5b474431d7248520d69))
* **provider:** route zai coding plan via anthropic-compatible endpoint ([#171](https://github.com/baphled/FlowState/issues/171)) ([c426fb3](https://github.com/baphled/FlowState/commit/c426fb3d4807054c77d71d67a9dabb780da9e2e8))
* **runtime:** skill loader and event-conditioned hook runner ([#183](https://github.com/baphled/FlowState/issues/183)) ([9e66c05](https://github.com/baphled/FlowState/commit/9e66c0548009d1b7bf12d81b62d95f15d6414982))
* **session:** add --model override to session run ([#157](https://github.com/baphled/FlowState/issues/157)) ([16e9f8f](https://github.com/baphled/FlowState/commit/16e9f8f24681287c6572da42c4778c42a2203cf7))
* **session:** add fresh-harness session/turn primitives with ordered events ([1d039e5](https://github.com/baphled/FlowState/commit/1d039e538856991b8abe06780556a0bd973ba5fd))
* **session:** add live self-test task mode to session run ([#154](https://github.com/baphled/FlowState/issues/154)) ([b04c351](https://github.com/baphled/FlowState/commit/b04c3516a790cd692e0bd7ef890a611841699cca))
* **session:** add session cancel and sigint exit 130 ([#143](https://github.com/baphled/FlowState/issues/143)) ([a5b08e3](https://github.com/baphled/FlowState/commit/a5b08e363268b68c72391ee6bcd9b13f38475515))
* **session:** add session/turn primitives with ordered events ([e0cbe39](https://github.com/baphled/FlowState/commit/e0cbe397f2bf60462c5ad3a55cf5eabf0ece9c15)), closes [#19](https://github.com/baphled/FlowState/issues/19)
* **session:** consumer-side session repo and event log ports ([#45](https://github.com/baphled/FlowState/issues/45)) ([f36345d](https://github.com/baphled/FlowState/commit/f36345d48e4c24946cca50a8a23c2d588ef89e0a))
* **session:** engine turn loop runs real provider turn with ordered events ([#50](https://github.com/baphled/FlowState/issues/50)) ([6539607](https://github.com/baphled/FlowState/commit/65396071d9c958bafe53f2e66e32593923069b11))
* **storage:** add receipts-table insert accessor ([#145](https://github.com/baphled/FlowState/issues/145)) ([6bbbb6c](https://github.com/baphled/FlowState/commit/6bbbb6c101fc19120fd5b02e406b88530212c9b8))
* **storage:** index receipt_ref on verified receipt insertion ([#148](https://github.com/baphled/FlowState/issues/148)) ([8547cf4](https://github.com/baphled/FlowState/commit/8547cf4925d851fe18ebd3a229453ce99befb33a))
* **store:** add durable agent item leases with fencing tokens ([#161](https://github.com/baphled/FlowState/issues/161)) ([8d6637b](https://github.com/baphled/FlowState/commit/8d6637b599f8f70c699a6c61105f452e75ccb317))
* **store:** add replay transcript and receipt validate commands ([#144](https://github.com/baphled/FlowState/issues/144)) ([1cc6d53](https://github.com/baphled/FlowState/commit/1cc6d53bec16942288260de5dd0aef1fe3eafb97)), closes [#nosec](https://github.com/baphled/FlowState/issues/nosec)
* **store:** add transactional event writer pause and resume ([#76](https://github.com/baphled/FlowState/issues/76)) ([dcc5d85](https://github.com/baphled/FlowState/commit/dcc5d85f6e86c4c570f1028206715bbc9bcc364a))
* **store:** emit member receipts for any admitted swarm packet ([#182](https://github.com/baphled/FlowState/issues/182)) ([741b060](https://github.com/baphled/FlowState/commit/741b0600312530c429be7e9705938b9d3e131b4a))
* **tests:** add end-to-end cli contract matrix suite ([677f0b7](https://github.com/baphled/FlowState/commit/677f0b717be99062a715d9b4a33d6318a12f0e12))
* **tests:** add m1 receipt schema validation ([#72](https://github.com/baphled/FlowState/issues/72)) ([981f276](https://github.com/baphled/FlowState/commit/981f276a4b8753905d521ae324173b02dac6e622)), closes [#71](https://github.com/baphled/FlowState/issues/71)
* **tests:** va-d checks, exit matrix and projection support ([#111](https://github.com/baphled/FlowState/issues/111)) ([e3bc588](https://github.com/baphled/FlowState/commit/e3bc58811bf5fce157c06275b2c822d4bddab706)), closes [#73](https://github.com/baphled/FlowState/issues/73)
* **tools:** add exec.run session tool ([#214](https://github.com/baphled/FlowState/issues/214)) ([c4daef4](https://github.com/baphled/FlowState/commit/c4daef4b6110ebee3436ae5d2b5232912473ef1e))
* **tools:** add fs read/write tools with workdir confinement ([#209](https://github.com/baphled/FlowState/issues/209)) ([98d3269](https://github.com/baphled/FlowState/commit/98d32691d450bc992b810197a8161f4518e24faa))
* **tools:** add fs.edit session tool ([#213](https://github.com/baphled/FlowState/issues/213)) ([e60052f](https://github.com/baphled/FlowState/commit/e60052f8381c172dc5067bae688270c6dafaf5c1))
* **tools:** wire fs tools into session cli via app layer ([#210](https://github.com/baphled/FlowState/issues/210)) ([8ce87cc](https://github.com/baphled/FlowState/commit/8ce87cc4d1351af1f4f7340cb61358020e6f04c8))
* **workflow:** add exact-head review handoff ([#265](https://github.com/baphled/FlowState/issues/265)) ([dd04ea2](https://github.com/baphled/FlowState/commit/dd04ea252b4e5bcfbe0bbb568d00e9218d1d02af))
* **workflow:** validate local dropbox config and route reads via localdropbox ([#277](https://github.com/baphled/FlowState/issues/277)) ([b5d0467](https://github.com/baphled/FlowState/commit/b5d046743a1b5ec84c00f7f4952e003446bd0141))

### Bug Fixes

* **app:** classify unknown tool surface as usage, not provider error ([2b79b25](https://github.com/baphled/FlowState/commit/2b79b25db364c33b0c70f1020db70b3874e4ccd7))
* **app:** session commands wire app, not session or store ([#290](https://github.com/baphled/FlowState/issues/290)) ([0aeb26a](https://github.com/baphled/FlowState/commit/0aeb26aa0fc498410af83b6cd381c48c07e4b025))
* **ci:** authenticate reviewer signatures and execution bindings ([#238](https://github.com/baphled/FlowState/issues/238)) ([9a800da](https://github.com/baphled/FlowState/commit/9a800daf4d00c37578db3c2c439ad09ec1b05ccb))
* **ci:** carry the pr number in the squash merge subject ([#289](https://github.com/baphled/FlowState/issues/289)) ([1fd7d4b](https://github.com/baphled/FlowState/commit/1fd7d4b8322308be8db1aeb90f6d3208807570c1))
* **ci:** enforce required pr policy checks in build ([#193](https://github.com/baphled/FlowState/issues/193)) ([1993e82](https://github.com/baphled/FlowState/commit/1993e829a1dce185bc9970af9e5ce0f9fc627f20))
* **ci:** enforce trusted python source quality ([#236](https://github.com/baphled/FlowState/issues/236)) ([b01168f](https://github.com/baphled/FlowState/commit/b01168ff2ec16ef47382b485190186e946fd9978))
* **ci:** execute owning tests from committed tree snapshot ([#257](https://github.com/baphled/FlowState/issues/257)) ([4dd4744](https://github.com/baphled/FlowState/commit/4dd474452ac0b93f9c63f4555bdc3a19decbbe36))
* **ci:** issue scoped ephemeral app status credentials ([#239](https://github.com/baphled/FlowState/issues/239)) ([941fd02](https://github.com/baphled/FlowState/commit/941fd02f608cdee67ccf4b4cb7140ffbec487dd3))
* **ci:** prevent untrusted PR text in workflows ([#192](https://github.com/baphled/FlowState/issues/192)) ([374cb3d](https://github.com/baphled/FlowState/commit/374cb3df19893c0063d839eaf3fa8fc1b067ce71))
* **ci:** reject skipped owner tests in python contract ([#258](https://github.com/baphled/FlowState/issues/258)) ([589693f](https://github.com/baphled/FlowState/commit/589693fd1f99ba9e35f6b6ed7f9a5b83f4535f78))
* **ci:** reject squash tips without project metadata trailer ([f8ccf42](https://github.com/baphled/FlowState/commit/f8ccf4269508897705f5aa8cd659fda5794a194b))
* **ci:** reject validation-time admission state drift ([#237](https://github.com/baphled/FlowState/issues/237)) ([97e32fb](https://github.com/baphled/FlowState/commit/97e32fbd26f6eee0d1517da2fbe079fa2c4019e3))
* **ci:** repair squash merge body line length ([ed1df96](https://github.com/baphled/FlowState/commit/ed1df96178f5213f71d654ae89a0583a89871faa))
* **ci:** require authenticated review on admission success ([#240](https://github.com/baphled/FlowState/issues/240)) ([c27a0eb](https://github.com/baphled/FlowState/commit/c27a0ebc14e580d375c39f2c278edae61fd82f15))
* **ci:** restore single project-metadata trailer on tip ([4bf4ddf](https://github.com/baphled/FlowState/commit/4bf4ddf7c78808c3c495913fe2f046616e2c71e4))
* **ci:** specific error for merge commits with multiple project-metadata trailers ([#206](https://github.com/baphled/FlowState/issues/206)) ([7ab5521](https://github.com/baphled/FlowState/commit/7ab5521fd8d8ec56f977b8d0c6c5d2a4ef02ee35))
* **ci:** structural squash trailer merge admission policy ([47005a9](https://github.com/baphled/FlowState/commit/47005a9dfe36b6095aa57ea2292249e5cdda6a93))
* **ci:** supply project-metadata trailer on squash merges ([f7195dd](https://github.com/baphled/FlowState/commit/f7195dd1e9122f246812a51b7dff4b29505e160a))
* **ci:** use first parent base for push events in next ci workflow ([#197](https://github.com/baphled/FlowState/issues/197)) ([988a5a0](https://github.com/baphled/FlowState/commit/988a5a0baa8581e7c710e703c6744412894da66c))
* **ci:** validate live pull request titles on reruns ([#280](https://github.com/baphled/FlowState/issues/280)) ([00df6a1](https://github.com/baphled/FlowState/commit/00df6a151dfccbbe85c321ce1b677ee8881e1b3a))
* **ci:** verify required checks on exact head before admission success ([#244](https://github.com/baphled/FlowState/issues/244)) ([3541796](https://github.com/baphled/FlowState/commit/3541796c10ca8ad7125394d9ec830b6ca03b1788))
* **deps:** update go toolchain to 1.26.10 ([#203](https://github.com/baphled/FlowState/issues/203)) ([19a1cf0](https://github.com/baphled/FlowState/commit/19a1cf0cbd30f1be0efd2169188d58ec95f81686))
* **lint:** enforce orm-only persistence in changed code ([#184](https://github.com/baphled/FlowState/issues/184)) ([872f95f](https://github.com/baphled/FlowState/commit/872f95f3e58ee576854aa69232cff37bb4a20720))
* **provider:** default z.ai model target to glm-5.3 ([#169](https://github.com/baphled/FlowState/issues/169)) ([bde9573](https://github.com/baphled/FlowState/commit/bde9573302f755be21c56ceea604ad17cc152c57))
* **provider:** default zai plan to coding route ([#204](https://github.com/baphled/FlowState/issues/204)) ([0f738fb](https://github.com/baphled/FlowState/commit/0f738fb4904e6bef40152b87ae628c17f9bf3019))
* **provider:** floor clamped max_tokens at 8192 ([#178](https://github.com/baphled/FlowState/issues/178)) ([4b33ad9](https://github.com/baphled/FlowState/commit/4b33ad9e453dfec8e7831311b2f591da7f5ccd00))
* **provider:** infer anthropic route from config host ([#177](https://github.com/baphled/FlowState/issues/177)) ([ba9146a](https://github.com/baphled/FlowState/commit/ba9146a4412c6a78a143e7978bc76c41e3b0694d)), closes [#176](https://github.com/baphled/FlowState/issues/176)
* **provider:** route coding plan to anthropic base url and clamp max_tokens ([#172](https://github.com/baphled/FlowState/issues/172)) ([0661884](https://github.com/baphled/FlowState/commit/0661884f0eb8ff4a0ac6e2192ddf56989686df9e))
* **provider:** serialise message with lowercase json keys ([#156](https://github.com/baphled/FlowState/issues/156)) ([09cfc79](https://github.com/baphled/FlowState/commit/09cfc7919b80180836f8a0787745d2afa5010893))
* **runtime:** fail closed on rejected swarm packets and missing receipts ([#194](https://github.com/baphled/FlowState/issues/194)) ([5ce6b3d](https://github.com/baphled/FlowState/commit/5ce6b3d8c523df052a46fc7e9e31297e70f57340))
* **runtime:** reject invalid swarm prerequisites before scheduling ([#199](https://github.com/baphled/FlowState/issues/199)) ([fc76d06](https://github.com/baphled/FlowState/commit/fc76d0621dd9a2cafc1b19f7e0748a4fc9b5b3e6))
* **runtime:** reject widened child scope during admission ([#195](https://github.com/baphled/FlowState/issues/195)) ([79ca786](https://github.com/baphled/FlowState/commit/79ca786cb4d7ef59d04dd4382b004c137addf4ee))
* **security:** bump go 1.26.11 and add interim merge-admission producer ([#243](https://github.com/baphled/FlowState/issues/243)) ([e03f2ea](https://github.com/baphled/FlowState/commit/e03f2ea36b41fd10c5d81bcd8b2f0e663bcda7bf)), closes [#241](https://github.com/baphled/FlowState/issues/241) [#241](https://github.com/baphled/FlowState/issues/241) [#225](https://github.com/baphled/FlowState/issues/225) [#225](https://github.com/baphled/FlowState/issues/225)
* **security:** scan next with branch go version ([#196](https://github.com/baphled/FlowState/issues/196)) ([8856bc3](https://github.com/baphled/FlowState/commit/8856bc31dba757bed39877cdd4f0f846309a981a))
* **store:** record truthful session update timestamps ([#63](https://github.com/baphled/FlowState/issues/63)) ([7c57e2d](https://github.com/baphled/FlowState/commit/7c57e2d10b3c0e880a5c0a7c2770214ae9cc19b9))
* **store:** restore paused writers as paused ([#137](https://github.com/baphled/FlowState/issues/137)) ([7cfc097](https://github.com/baphled/FlowState/commit/7cfc09789072e86737158a3baf43ab9ff07867f5))
* **tests:** enforce implementation test-file ownership ([#189](https://github.com/baphled/FlowState/issues/189)) ([a334fcc](https://github.com/baphled/FlowState/commit/a334fcc191356aca7eed72bfa8e7b6fa335587e7))
* **tests:** make the sigint e2e scenario platform-robust ([b21ffd6](https://github.com/baphled/FlowState/commit/b21ffd6a4493c25ac2ed34e6174428c6b1dc6aaf))
* **tests:** reject vacuous and bundled test oracles ([#64](https://github.com/baphled/FlowState/issues/64)) ([1e7be3a](https://github.com/baphled/FlowState/commit/1e7be3a712ee479e60dec5018f7edd17d0033fd4))
* **tools:** advertise the exec allowlist policy at definition and denial ([2b37e33](https://github.com/baphled/FlowState/commit/2b37e339603191daeb91d4af90bffdf291f8206e))
* **workflow:** enforce external metadata and truthful commits ([#62](https://github.com/baphled/FlowState/issues/62)) ([f85ef4a](https://github.com/baphled/FlowState/commit/f85ef4a513aae7b8a3b39c78131125db4cf3844b))
* **workflow:** fail visibly for missing admission result ([#275](https://github.com/baphled/FlowState/issues/275)) ([4dc1c15](https://github.com/baphled/FlowState/commit/4dc1c15c82901818d191b55450f6e42213867d1f))
* **workflow:** package and supervise the admission runner ([#274](https://github.com/baphled/FlowState/issues/274)) ([7e09a34](https://github.com/baphled/FlowState/commit/7e09a346632b6cd248bf8a949127460098db70df))
* **workflow:** pin admission monitor to session startup v2 ([#276](https://github.com/baphled/FlowState/issues/276)) ([be6bc73](https://github.com/baphled/FlowState/commit/be6bc73644da342e83d6b785306f1d39fc4827f3))
* **workflow:** select native flowstate verifier without fallback ([#281](https://github.com/baphled/FlowState/issues/281)) ([962be49](https://github.com/baphled/FlowState/commit/962be49303bcd2c6e77a09dca1ca3cc3aeffc06e))
* **workflow:** validate actual admission evidence before push ([#268](https://github.com/baphled/FlowState/issues/268)) ([8fda247](https://github.com/baphled/FlowState/commit/8fda247f5767a7f605d1ca4596df2103968bd267))

### Code Refactoring

* **app:** absorb the internal-app ports into internal/app ([#125](https://github.com/baphled/FlowState/issues/125)) ([af9208c](https://github.com/baphled/FlowState/commit/af9208cf047a145535639873cfc9167f9857224e))
* **app:** remove internal/validation entirely ([#134](https://github.com/baphled/FlowState/issues/134)) ([2631ddd](https://github.com/baphled/FlowState/commit/2631dddff79683f1aefbbb448ebc5f7c99df8648))
* **app:** retire the validation checks surface ([#133](https://github.com/baphled/FlowState/issues/133)) ([bddb727](https://github.com/baphled/FlowState/commit/bddb7274119a19ec1b45d6086737e8765a9b626c))
* **app:** retire the validation projection and receipt-index specs ([#129](https://github.com/baphled/FlowState/issues/129)) ([1ac1fb2](https://github.com/baphled/FlowState/commit/1ac1fb25db3be697bc5381b0f6b6eedab8822565)), closes [#124](https://github.com/baphled/FlowState/issues/124)
* **app:** retire the validation receipt surface ([#132](https://github.com/baphled/FlowState/issues/132)) ([0e63827](https://github.com/baphled/FlowState/commit/0e63827497c1d0e79a5c20024a60b8731b2a1800)), closes [#124](https://github.com/baphled/FlowState/issues/124)
* **build:** decompose project metadata module into contract-compliant modules ([5715114](https://github.com/baphled/FlowState/commit/5715114e0981d1873d236cb03d3bcaf36f959979))
* **ci:** bound merge admission source ownership ([#227](https://github.com/baphled/FlowState/issues/227)) ([0392a9a](https://github.com/baphled/FlowState/commit/0392a9adab1c509a7b197871fc6f65baf5ab1720))
* **session:** absorb session list/update into store and drop db accessor ([#44](https://github.com/baphled/FlowState/issues/44)) ([12181b0](https://github.com/baphled/FlowState/commit/12181b06008937cb71f435b6390db3159cf90a2b))
* **session:** remove dogfood run command and route ([#153](https://github.com/baphled/FlowState/issues/153)) ([4da2495](https://github.com/baphled/FlowState/commit/4da24950f33dcf1b0f08b857e421424d4795bf6a))
* **storage:** adopt orm for swarm persistence ([#187](https://github.com/baphled/FlowState/issues/187)) ([cf4c6e2](https://github.com/baphled/FlowState/commit/cf4c6e2f5b1a02b89903677b30b530d8e5d1e0e9))
* **tests:** retire the validation exit matrix and check helpers ([#124](https://github.com/baphled/FlowState/issues/124)) ([1570ea9](https://github.com/baphled/FlowState/commit/1570ea9a5bd2dcbb3afa9ce1e0a76f7c8e8fb150))

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
