# sashiki-todo-demo

[sashiki](https://github.com/rikukaInoue/sashiki) を使ったアプリケーション開発のデモ。
Go の TODO アプリ(Lambda Web Adapter)に **PR ごとのプレビュー環境**が付く。
DB は RDS の代わりに sashiki の DB ブランチ — アプリは `DB_HOST/PORT/USER` を env で受けるだけの普通の HTTP アプリで、sashiki を知らない。

このデモの設計・AWS 構成・実運用で踏んだバグの解説記事: [PoC から OSS へ — MySQL のデータベースブランチ基盤『sashiki』を作って公開した](https://rikuka.dev/blog/db-branch-sashiki-oss/)

PR を開くと:

1. sashiki が DB ブランチ `pr-<N>` を数秒で生やす(CoW クローン、実データ入り)
2. PR の `migrations/*.sql` が**その PR のブランチにだけ**適用される(他 PR・baseline は無傷)
3. `https://pr-<N>.sashiki-demo.rikuka.dev/` が PR にコメントされ、ブラウザで開ける

PR を閉じると DB ブランチは削除される(アプリは共有なので残る)。

## アーキテクチャ(共有 Lambda + サブドメイン方式)

```
Pull Request ──▶ GitHub Actions (self-hosted runner = sashiki サーバーの EC2 上)
                   ├─▶ sashiki action ──▶ sashikid   DB ブランチ pr-<N> を create/delete
                   └─▶ migrations/*.sql を PR のブランチへ適用(冪等、_migrations で記録)

ブラウザ ──▶ *.sashiki-demo.rikuka.dev(ワイルドカード DNS)
              └─▶ 共有 CloudFront(1つ)
                    │  CloudFront Function: Host → x-forwarded-host
                    └─▶ 共有 API Gateway → 共有 Lambda(LWA コンテナ、1つ)
                          │  アプリがサブドメインから branch を判定(MULTI_BRANCH)
                          └─▶ sashiki プロキシ :3306 に dev@pr-<N> で接続 → mysqld@pr-<N>
```

**PR ごとに作るのは DB ブランチだけ**なので、プレビュー環境の作成は数秒で終わる
(アプリのビルド・デプロイは走らない)。アプリコードが変わったときだけ
`deploy-shared.yml` が共有 Lambda を更新する。

## 3 つの動作モード

| モード | 有効化 | アプリの配置 | 用途 |
|---|---|---|---|
| **共有 Lambda(推奨)** | `vars.PREVIEW_DOMAIN` | AWS に 1 個(`sashiki-todo-shared` スタック) | `https://pr-N.<domain>/` を PR にコメント。作成数秒 |
| PR ごとスタック | `vars.AWS_REGION`(PREVIEW_DOMAIN 空) | PR ごとに SAM スタック | アプリコードも PR ごとに変えたい場合。作成数分 |
| ローカル | `vars.LOCAL_PREVIEW=true` | runner ホストの systemd unit | AWS なしの動作確認(URL は runner ホスト限定) |

モードは排他に設定すること(PREVIEW_DOMAIN を設定したら LOCAL_PREVIEW は false に)。

## リポジトリの Variables / Secrets

| 種別 | 名前 | 内容 |
|---|---|---|
| Variable | `SASHIKI_API_URL` | sashikid の API URL(runner が同居なら `http://127.0.0.1:8080`) |
| Secret | `SASHIKI_API_TOKEN` | sashiki API トークン(runner が loopback なら不要) |
| Variable | `PREVIEW_DOMAIN` | 共有 Lambda モードのベースドメイン(例 `sashiki-demo.rikuka.dev`) |
| Variable | `SASHIKI_DB_HOST` | 共有 Lambda が接続する DB ホスト(例 `sashiki.internal`。Route53 private zone) |
| Variable | `AWS_REGION` | 例 `ap-northeast-1`(AWS 系ジョブのゲート) |
| Variable | `PREVIEW_SUBNET_IDS` | Lambda を置くサブネット(カンマ区切り) |
| Variable | `PREVIEW_SECURITY_GROUP_IDS` | sashiki サーバーの 3306-3600 へ届く SG |
| Variable | `AWS_ROLE_ARN` | (任意)OIDC ロール。無ければ runner の instance profile を使う |
| Variable | `LOCAL_PREVIEW` | ローカルモードの有効化(`true`) |
| Variable | `PREVIEW_ALLOWED_ACTORS` | プレビューを作れる PR 作成者の許可リスト(カンマ区切り。**未設定だと誰も作れない**) |

## リポジトリ外の共有インフラ(手動セットアップ)

共有 Lambda モードは以下に依存する(このリポジトリの IaC には含まれない。TODO: IaC 化):

- **sashiki サーバー**: EC2 上に sashikid + baseline(`baseline/schema.sql` を投入して
  正常終了状態で snapshot を取得)+ **同じホストに self-hosted runner**(`vpc` ラベル)。
  API は loopback のまま使える
- **Route53 private hosted zone**(例 `sashiki.internal` → EC2 private IP)— PR コメントや
  Lambda の接続先に生 IP を出さないため
- **ワイルドカード ACM 証明書(us-east-1)+ 共有 CloudFront**: alias `*.<PREVIEW_DOMAIN>`、
  origin = 共有 API Gateway、viewer-request の CloudFront Function で
  `Host` を `x-forwarded-host` にコピー
- **Route53**: `<PREVIEW_DOMAIN>` と `*.<PREVIEW_DOMAIN>` を CloudFront へ alias

認証を付けるなら CloudFront の viewer-request に Google OIDC を足す構成が使える:
[PR ごとのプレビュー環境を Lambda@Edge + Google OIDC で保護する](https://rikuka.dev/blog/pr-preview-lambda-edge-google-oidc/)

## マイグレーション(PR ごとのスキーマ)

- `migrations/*.sql` を PR に含めると、workflow がステージ(`/var/lib/sashiki/pr-src/pr-<N>/`)し、
  **sashiki の on-create hook がブランチ作成時に適用**する(適用済みは `_migrations` で記録)。
  hook は `@init` 取得前に走るため **`sashiki reset` しても migration は保持**され、
  synchronize で migrations が変わった場合は自動で recreate → 再適用される
- アプリはスキーマ差分に耐える作り(例: `todos.priority` は存在するときだけ表示)
- **PR を merge すると `baseline-refresh.yml` が自動で baseline を更新する**
  (migrations/ を `/etc/sashiki/baseline-src/` に同期 → サーバー側 refresh.sh が
  `_migrations` 記録つきで冪等適用 → build→validate→publish で current 切替)。
  以後の新しい PR は新スキーマの baseline から生える
- **既に開いている PR のブランチは影響を受けない**(origin 固定)。最新 baseline に
  追従したい PR だけ `sashiki recreate pr-N` + workflow 再実行(git rebase main 相当)
- 手動で回すときは Actions の baseline-refresh を workflow_dispatch、
  切替の巻き戻しは `sashiki baseline set <旧snapshot>`、旧世代の回収は `sashiki baseline gc`

## ローカル開発

```bash
docker compose up --build
open http://localhost:8080
```

MySQL には `baseline/schema.sql` が initdb として投入される。`migrations/` は自動適用
されないので、必要なら `mysql -h127.0.0.1 -uroot -proot todo < migrations/xxx.sql` で。

## 構成ファイル

| ファイル | 役割 |
|---|---|
| `main.go` / `templates/` | TODO アプリ(単一ブランチ / MULTI_BRANCH の 2 モード) |
| `Dockerfile` | Lambda Web Adapter 同梱のコンテナ(alpine ベース) |
| `template.yaml` | SAM テンプレート(共有 / PR ごと両モードで使用) |
| `.github/workflows/preview.yml` | PR 連動(db / app-shared / app / app-local / cleanup) |
| `.github/workflows/deploy-shared.yml` | 共有 Lambda のデプロイ(アプリ変更時) |
| `baseline/schema.sql` | ベースライン用スキーマ + シード(compose の initdb 兼用) |
| `migrations/` | PR ごとに適用されるマイグレーション |

## 公開(public 化)時の注意

このリポジトリは **self-hosted runner** を使うため、public にする場合は必ず:

1. ワークフローの全ジョブにあるフォーク除外ガード
   (`github.event.pull_request.head.repo.full_name == github.repository`)を外さないこと。
   フォーク PR のコードが runner(= sashiki サーバーの EC2、AWS 権限つき)で実行されるのを防ぐ
2. Settings → Actions → 「Fork pull request workflows」で **全ての外部コントリビューターに承認を必須**にする
3. sashiki 側の上限(`max_branches` / TTL)をデモ向けに絞っておく(承認済み PR でもリソースは有界)

フォークからの PR はプレビューが作られない(ガードで skip される)。さらに同一リポの PR でも
作成者が `PREVIEW_ALLOWED_ACTORS` に居なければ skip される(二重の許可リスト)。
PR の受付自体を止めたい場合は interaction limits(`collaborators_only`、最長 6 ヶ月・更新可)を使う:
`gh api repos/<owner>/<repo>/interaction-limits -X PUT -f limit=collaborators_only -f expiry=six_months`
