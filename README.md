# sashiki-todo-demo

[sashiki](https://github.com/rikukaInoue/sashiki) を使ったアプリケーション開発のデモ。
Go の TODO アプリに **PR ごとの使い捨てプレビュー環境**(アプリ = Lambda、DB = sashiki のブランチ)が付く。

PR を開くと:

1. sashiki が DB ブランチ `pr-<N>` を数秒で生やす(CoW クローン、実データ入り)
2. SAM が Lambda (Web Adapter) を PR 専用スタックとしてデプロイ
3. PR にプレビュー URL と MySQL 接続先がコメントされる

PR を閉じると、アプリのスタックも DB ブランチも削除される。

```
Pull Request ──▶ GitHub Actions (self-hosted, VPC 内)
                   │
                   ├─▶ sashiki action ──HTTP──▶ sashikid     DB ブランチ pr-<N> を create/delete
                   │                          └─ mysqld@pr-<N> (ZFS CoW クローン)
                   │
                   └─▶ sam deploy ──▶ CloudFormation スタック sashiki-todo-pr-<N>
                                        └─ Lambda (コンテナ + Lambda Web Adapter)
                                            ├─ Function URL = プレビュー URL
                                            └─ VPC 内から mysqld@pr-<N> に接続
```

アプリ自体は sashiki を知らない。`DB_HOST` / `DB_PORT` / `DB_USER` を環境変数で受け取る
だけの普通の HTTP アプリで、Lambda Web Adapter を extension として同梱しているので
ローカルでも Lambda でも同じイメージが動く。

## ローカルで動かす

```bash
docker compose up --build
open http://localhost:8080
```

MySQL には `baseline/schema.sql` が initdb として投入される(プレビュー環境で
sashiki のベースラインから生えてくるのと同じスキーマ + シード)。

コンテナを使わない場合:

```bash
go run . # DB_HOST などは環境変数で指定
```

## プレビュー環境のセットアップ

### 1. sashiki サーバー側

sashiki の README に従って `sashiki init` 済みのサーバーを用意し、ベース mysqld に
`baseline/schema.sql` を投入してから、mysqld を正常終了させた状態で @init
スナップショットを取得する。sashikid の API が Actions ランナーから届くこと。

### 2. リポジトリの Variables / Secrets

| 種別 | 名前 | 内容 |
|---|---|---|
| Variable | `SASHIKI_API_URL` | sashikid の API URL(例 `http://sashiki.internal:8080`) |
| Secret | `SASHIKI_API_TOKEN` | sashiki API トークン |
| Variable | `AWS_ROLE_ARN` | GHA の OIDC で assume する IAM ロール |
| Variable | `AWS_REGION` | 例 `ap-northeast-1` |
| Variable | `PREVIEW_SUBNET_IDS` | Lambda を置くサブネット(カンマ区切り) |
| Variable | `PREVIEW_SECURITY_GROUP_IDS` | sashiki サーバーの MySQL ポート帯(3400〜)へ egress できる SG |

IAM ロールには CloudFormation / Lambda / ECR / IAM(サービスロール作成)/
EC2(ENI 管理)の権限が必要。`sam deploy --resolve-image-repos --resolve-s3` が
ECR リポジトリと S3 バケットを自動作成する。

### 3. ランナー

`runs-on: [self-hosted, vpc]` — sashikid に届く VPC 内の self-hosted ランナーに
`sam` / `docker` / `aws` / `gh` を入れておく。
sashiki が private リポジトリの間は、Actions の設定で organization / user 内の
private action(`rikukaInoue/sashiki/action`)へのアクセスを許可しておくこと。

### 4. 動作確認

PR を開く → 数分でプレビュー URL がコメントされる。
`ALTER TABLE` や `DROP TABLE` を試すマイグレーション PR でも、壊れるのは
その PR のブランチだけ。`sashiki reset pr-<N>` で作成時点に戻せる。

## 構成ファイル

| ファイル | 役割 |
|---|---|
| `main.go` / `templates/` | TODO アプリ本体(net/http + html/template) |
| `Dockerfile` | マルチステージビルド + Lambda Web Adapter 同梱 |
| `compose.yaml` | ローカル開発(MySQL 付き) |
| `template.yaml` | SAM: プレビュー 1 環境分(Lambda + Function URL) |
| `.github/workflows/preview.yml` | PR open/close に連動したデプロイ / 削除 |
| `baseline/schema.sql` | ベースライン用スキーマ + シード |

## 注意

- Function URL は `AuthType: NONE`(デモ用)。実運用では IAM 認証や CloudFront を挟む
- Actions の closed イベントは取りこぼすことがあるため、DB ブランチの削除は
  sashiki 側の TTL 自動回収(sashiki issue #8)と併用する。アプリ側のスタックも
  同様に残ることがあるので、`sashiki-todo-pr-*` スタックの定期棚卸しを推奨

<!-- verify #71 -->
