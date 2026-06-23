# AutoStream Control Panel Host Install

This archive contains the Linux binary, systemd example, placeholder environment file, and built web assets for the AutoStream Control Panel.

## Requirements

- Linux amd64 or arm64 matching the archive name.
- A dedicated `autostream` user and group.
- A reverse proxy with HTTPS for production.
- A production database and secret values supplied outside Git.

## Install

```bash
sudo install -o root -g root -m 0755 bin/control-panel /usr/local/bin/control-panel
sudo install -d -o autostream -g autostream /var/lib/autostream/control-panel
sudo install -d -o root -g root /usr/share/autostream-control-panel
sudo cp -a share/autostream-control-panel/. /usr/share/autostream-control-panel/
sudo install -o root -g root -m 0644 systemd/autostream-control-panel.service.example /etc/systemd/system/autostream-control-panel.service
sudo install -o root -g root -m 0640 .env.example /etc/autostream/control-panel.env
```

Edit `/etc/autostream/control-panel.env` with real environment-specific values, then run:

```bash
sudo systemctl daemon-reload
sudo systemctl enable --now autostream-control-panel
```

Do not commit real `.env` files, provider credentials, tokens, logs, screenshots, or verification record.
