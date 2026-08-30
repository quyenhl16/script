# Kiến trúc syssetup

## Nguyên tắc

- Go điều phối; Bash thay đổi hệ thống.
- Profile mô tả server cần feature nào.
- Feature package tự chứa manifest, script và asset.
- Runner không biết logic riêng của Nginx, Docker hay database.
- Feature phải idempotent và có thể kiểm tra trước khi thay đổi.
- Workflow chỉ điều phối feature; không chứa shell command hoặc logic hệ thống.

## Luồng thực thi

```text
profile JSON
    |
    v
registry.Load(features/)
    |
    v
validate manifest + parameters
    |
    v
resolve dependency DAG
    |
    v
check --(exit 10)--> apply --> verify
    |
    v
result + log
```

Remote workflow dùng luồng riêng:

```text
workflow config + workflow manifest
    -> validate ordered dependencies
    -> expand step invocations / derive arguments / load local artifacts
    -> parallel servers
    -> sequential dependent steps per server
    -> per-server step results
```

Dependency luôn chạy trước feature phụ thuộc và chỉ xuất hiện một lần trong execution plan.

## Ranh giới package Go

- `domain`: model không phụ thuộc infrastructure.
- `config`: đọc profile.
- `registry`: khám phá feature, validation và dependency resolution.
- `platform`: nhận diện `/etc/os-release` và quyền root.
- `runner`: timeout, process execution, output và log.
- `workflow`: khám phá workflow, validate config, derive output-to-input và điều
  phối remote feature theo từng server.
- `ui`: dashboard Bubble Tea gồm bảng feature, execution plan, live logs và
  panel chi tiết; chỉ gọi public API của registry/runner.
- `cmd/syssetup`: parse CLI và ghép các component.

## Feature API v1

Manifest dùng `apiVersion: syssetup/v1`. Entrypoint nhận đúng một action:

```bash
run.sh check
run.sh apply
run.sh verify
run.sh rollback
```

Runner truyền parameter dưới dạng `SYSSETUP_PARAM_<NAME>`. Không dùng `eval`, không ghép parameter vào câu lệnh và luôn đặt biến trong dấu nháy kép.

## Workflow API v1

Manifest dùng `apiVersion: syssetup/workflow/v1`. Mỗi step tham chiếu một
`remoteOnly` feature và chỉ được phụ thuộc step đã khai báo trước. Config
`syssetup/workflow-config/v1` chứa danh sách invocation/args. `deriveArgs` cho
phép step sau thu thập argument có prefix từ config của step trước mà không lặp
lại dữ liệu, ví dụ lấy `gateway=` của `create-vlan` cho `verify-network`.

Mỗi invocation có thể khai báo `artifacts` gồm `id` và local `source`. Executor
đọc và giới hạn kích thước file trước khi kết nối, mã hóa nội dung vào stdin của
SSH, tạo file tạm với `umask 077`, export đường dẫn qua biến
`SYSSETUP_ARTIFACT_<ID>` và xóa toàn bộ thư mục tạm bằng `trap` sau khi feature
kết thúc. Đường dẫn tương đối được resolve từ thư mục chứa workflow config.

## Mở rộng ngoài repo

Có thể đặt feature ở thư mục khác và chạy:

```bash
syssetup list --features-dir /opt/company/features
syssetup run --features-dir /opt/company/features --profile company-server.json
```

Nhờ đó feature nội bộ có thể nằm trong repo riêng mà không fork core tool. API version cho phép phát triển manifest v2 sau này mà vẫn phát hiện cấu hình không tương thích rõ ràng.
