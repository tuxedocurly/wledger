---
title: Troubleshooting
sidebar_position: 3
---

# Troubleshooting & Bug Reports

If you encounter unexpected behavior, enable **Enable Debug Logs** in **Settings** and inspect your container or process output. Then file an [Issue](https://github.com/tuxedocurly/wledger/issues/new) on GitHub or reach out on the [WLEDger Discord](https://discord.com/invite/wmDdcar7Kq).

## Viewing Logs

WLEDger writes structured JSON logs directly to standard output (`stdout`):

```bash
docker logs --follow wledger
```

If you are running the single binary directly, inspect the terminal output or systemd journal (`journalctl -u wledger -f`).

## Common Issues

### Camera Barcode Scanner Blocked on Mobile Browser

Modern mobile browsers (iOS Safari, Android Chrome) require a secure HTTPS context for camera access (`navigator.mediaDevices.getUserMedia`).

* **Fix:** Set `WLEDGER_TLS_SELF_SIGNED=true` (and accept the generated LAN certificate once in your browser) or access WLEDger through an HTTPS reverse proxy (`WLEDGER_TRUST_PROXY=true` and `WLEDGER_COOKIE_SECURE=true`).

### WLED Controller Shows Offline After Router Reboot (DHCP IP Change)

* **Fix:** If the controller's DHCP IP changed, open **Hardware** and click **Discover WLED** — WLEDger matches discovered `_wled._tcp.local.` devices by MAC address and automatically updates the stored IP address. If the controller kept the same IP and simply rebooted, click the **Refresh Status** icon next to its **Offline** badge on **Hardware** (or trigger an HTTP **Locate** / wait for **Periodic Controller Status Ping**) to mark it **Online** immediately.

### Rate-Limited (`429 Too Many Requests`) Behind a Reverse Proxy

If all users appear to share the reverse proxy's IP address, set `WLEDGER_TRUST_PROXY=true` so WLEDger reads the client IP from `X-Forwarded-For` / `X-Real-IP`. Never enable `WLEDGER_TRUST_PROXY=true` if WLEDger is exposed directly without a trusted proxy in front of it.

### Docker Permission Errors on `./data` or `./uploads`

The Docker image runs as non-root user `wledger` (UID `10001`, GID `10001`). Ensure your mounted host `./data` and `./uploads` directories are writable by UID `10001` (`chown -R 10001:10001 ./data ./uploads` on Linux).
