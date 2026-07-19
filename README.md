# syssetup

`syssetup` là tool cấu hình hệ thống có thể mở rộng cho CentOS, RHEL, Rocky Linux và AlmaLinux. Go chịu trách nhiệm CLI/TUI, validation, dependency, timeout và logging; Bash thực hiện thay đổi hệ thống.

Tool không cần dependency Go bên ngoài và có thể build offline.

## Kiến trúc

```text
CLI / TUI
    -> profile loader
    -> feature registry
    -> dependency resolver
    -> runner: check -> apply -> verify
    -> Bash feature packages
    -> RHEL-compatible host
```

Mỗi thư mục `features/<id>/` là một package độc lập gồm `feature.json` và `run.sh`. Registry tự khám phá package, vì vậy thêm feature không yêu cầu sửa code Go.

## Bắt đầu

```bash
go test ./...
go build -o bin/syssetup ./cmd/syssetup

./bin/syssetup list
./bin/syssetup plan --profile profiles/base-server.json
./bin/syssetup run --profile profiles/base-server.json --dry-run
sudo ./bin/syssetup run --profile profiles/base-server.json
sudo ./bin/syssetup tui --profile profiles/base-server.json
```

Hoặc:

```bash
chmod +x setup.sh
sudo ./setup.sh tui
```

Log mặc định được ghi vào `syssetup.log`. Có thể đổi bằng `--log /var/log/syssetup.log`.

## Thêm feature

Tạo cấu trúc:

```text
features/nginx/
├── feature.json
├── run.sh
└── templates/
```

Manifest tối thiểu:

```json
{
  "apiVersion": "syssetup/v1",
  "id": "nginx",
  "name": "Nginx",
  "version": "1.0.0",
  "entrypoint": "run.sh",
  "supportedOS": ["rhel", "centos", "rocky", "almalinux"],
  "requireRoot": true,
  "timeoutSeconds": 300,
  "dependsOn": ["check-os"]
}
```

Entrypoint phải hỗ trợ bốn action:

- `check`: trả `0` nếu trạng thái đã đúng, `10` nếu cần chạy `apply`.
- `apply`: thực hiện thay đổi.
- `verify`: xác nhận trạng thái sau thay đổi.
- `rollback`: hoàn tác nếu feature hỗ trợ.

Sau đó thêm `{"id":"nginx"}` vào một file trong `profiles/`. Không cần đăng ký feature trong Go.

## Phân phối

```bash
make build-linux
```

Đóng gói binary trong `dist/` cùng `features/`, `profiles/` và `scripts/`. Máy đích chỉ cần Bash và các tiện ích hệ thống chuẩn; không cần cài Go nếu sử dụng binary release.

## An toàn

- Runner không dùng `bash -c`.
- Entrypoint không được thoát khỏi thư mục feature.
- Parameter được validate theo manifest và truyền qua environment.
- Feature có timeout riêng.
- Feature yêu cầu root được kiểm tra trước khi chạy.
- Script phải idempotent để có thể chạy lại an toàn.
