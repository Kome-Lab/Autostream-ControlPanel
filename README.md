# autostream-control-panel

AutoStream の central control plane です。

## 役割

- Web Control Panel と Control API。
- 認証、server-side session、CSRF、RBAC、TOTP MFA、Passkey / WebAuthn、監査ログ。
- users / roles / permissions / API tokens / secret status。
- stream lifecycle、service registry、heartbeat、assignment、start / stop / retry-upload dispatch。
- Encoder / Worker / Observability の status と診断情報の表示連携。
- Google Drive / YouTube / OAuth / notification などの integration registry。

重い media 処理、Discord 接続、FFmpeg 実行、Google Drive upload の実処理は別リポジトリの責務です。

## 主な環境変数

```text
DATABASE_URL=mysql://autostream:<PASSWORD>@tcp(127.0.0.1:3306)/autostream_control
AUTOSTREAM_PUBLIC_URL=https://control.example.com
AUTOSTREAM_SESSION_SECRET=<SESSION_SECRET>
AUTOSTREAM_SECRET_ENCRYPTION_KEY=<BASE64_32_BYTES>
SERVICE_CALL_TOKEN=<SERVICE_CALL_TOKEN>
AUTOSTREAM_SERVICE_PUBLIC_ALLOWED_HOSTS=encoder.example.com,worker.example.com,bot.example.com,observability.example.com
AUTOSTREAM_REQUIRE_SERVICE_PUBLIC_ALLOWED_HOSTS=true
TZ=Asia/Tokyo
```

Discord token、YouTube stream key、Google OAuth refresh token、webhook URL、SMTP password などの運用 secret は Control Panel の DB / encrypted secret 管理へ移す方針です。env は service 起動と bootstrap に必要な最小値だけにしてください。

## 開発

```powershell
go test ./...
go build ./...
cd web
npm install
npm run build
```

## Deployment

- Docker / Compose: `Dockerfile`、`docker-compose.yml`
- systemd unit: `systemd/autostream-control-panel.service.example`
- Detailed deployment and security documentation is maintained in the `autostream-docs` repository.

## Security

- raw secret は API response / frontend / log / audit metadata に出しません。
- secret UI は configured / missing / fingerprint のみを表示します。
- `SERVICE_CALL_TOKEN` は outbound dispatch 用です。service registration token と混同しません。
- `AUTOSTREAM_PUBLIC_URL` が HTTPS の場合、session cookie は `Secure` を有効にします。
- Passkey / WebAuthn の ceremony token は `Cache-Control: no-store` で返し、server-side では hash と session data だけを短時間保存します。
