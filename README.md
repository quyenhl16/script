# syssetup

`syssetup` là tool cấu hình hệ thống có thể mở rộng cho CentOS, RHEL, Rocky Linux và AlmaLinux. Go chịu trách nhiệm CLI/TUI, validation, dependency, timeout và logging; Bash thực hiện thay đổi hệ thống.

Giao diện dashboard sử dụng Bubble Tea, Bubbles và Lip Gloss. Dependency được quản lý bằng Go Modules; máy đích không cần Go nếu dùng release binary.

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
go mod download
go test ./...
go build -o bin/syssetup ./cmd/syssetup

./bin/syssetup list
./bin/syssetup plan --profile profiles/base-server.json
./bin/syssetup run --profile profiles/base-server.json --dry-run
sudo ./bin/syssetup run --profile profiles/base-server.json
sudo ./bin/syssetup tui --profile profiles/base-server.json
```

Kiểm tra VIP, OSPF và kết nối mạng của workload Kubernetes `pramf01`:

```bash
./bin/syssetup plan --profile profiles/pramf01-connectivity.json
./bin/syssetup run --profile profiles/pramf01-connectivity.json
```

Đổi `phase` trong profile thành `vip`, `ospf`, `ping` hoặc `all` để chọn
phạm vi kiểm tra. Namespace, VIP và địa chỉ NF đích đều có thể cấu hình trong
`parameters` của feature `k8s-connectivity-check`.

Hoặc:

```bash
chmod +x setup.sh
sudo ./setup.sh tui
```

## TUI dashboard

TUI chạy toàn màn hình theo phong cách dashboard quản trị như k9s:

- `Features`: bảng feature, trạng thái, version, quyền thực thi và panel chi tiết.
- `Plan`: thứ tự thực thi sau khi resolve dependency.
- `Logs`: output cập nhật trong lúc runner hoạt động.
- `Remote SSH`: nhập danh sách server, user, password để chạy Linux command
  hoặc gửi một script cục bộ lên nhiều server qua SSH.
- Layout tự thích nghi; panel chi tiết được ẩn trên terminal hẹp.

Phím tắt:

| Phím | Chức năng |
|---|---|
| `j/k`, `↑/↓` | Di chuyển |
| `Space`, `Enter` | Chọn hoặc bỏ chọn feature |
| `/` | Lọc feature |
| `a`, `n` | Chọn tất cả feature đang hiển thị hoặc bỏ chọn tất cả |
| `1/2/3/4`, `Tab` | Chuyển Features, Plan, Logs và Remote SSH |
| `r` | Chạy execution plan |
| `c` | Hủy plan đang chạy |
| `F2` | Đổi giữa chạy command và script trong Remote SSH |
| `F5` | Chạy SSH trên tất cả server đã nhập |
| `?` | Hiện trợ giúp |
| `q` | Thoát |

Trong tab `Remote SSH`, danh sách server dùng dấu phẩy hoặc khoảng trắng,
ví dụ `10.0.0.10, 10.0.0.11:2222`. Nếu không ghi port thì mặc định là `22`.
Các server chạy song song với timeout 30 giây/server và kết quả được hiển thị
riêng. Password chỉ được giữ trong bộ nhớ; host key được lưu theo cơ chế
trust-on-first-use tại thư mục cấu hình người dùng và bị từ chối nếu thay đổi ở
lần kết nối sau.

Khi build từ source cần Go 1.24 trở lên. Binary đã build vẫn có thể chạy độc lập trên server đích.

Log mặc định được ghi vào `syssetup.log`. Có thể đổi bằng `--log /var/log/syssetup.log`.
Các dòng `[PASS]` được tô xanh và `[FAIL]` được tô đỏ trên CLI/TUI; file log
vẫn lưu text thuần. Đặt biến môi trường `NO_COLOR=1` để tắt màu hiển thị.

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

Đóng gói đầy đủ runtime cho cả Linux `amd64` và `arm64`:

```bash
make package
```

Hoặc chỉ đóng gói một kiến trúc và tùy chọn version:

```bash
make package-linux-amd64 VERSION=0.2.0
make package-linux-arm64 VERSION=0.2.0
```

Kết quả:

```text
dist/
├── syssetup-0.2.0-linux-amd64.tar.gz
└── syssetup-0.2.0-linux-arm64.tar.gz
```

Mỗi archive chứa `bin/syssetup`, `features/`, `profiles/`, `scripts/`,
`setup.sh` và `README.md`. Giải nén trên server rồi chạy từ thư mục vừa tạo:

```bash
tar -xzf syssetup-0.2.0-linux-amd64.tar.gz
cd syssetup-0.2.0-linux-amd64
sudo ./setup.sh tui
```

Máy đích chỉ cần Bash và các tiện ích hệ thống chuẩn; không cần cài Go nếu sử dụng binary release.

## An toàn

- Runner không dùng `bash -c`.
- Entrypoint không được thoát khỏi thư mục feature.
- Parameter được validate theo manifest và truyền qua environment.
- Feature có timeout riêng.
- Feature yêu cầu root được kiểm tra trước khi chạy.
- Script phải idempotent để có thể chạy lại an toàn.
