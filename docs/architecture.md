# Kiến trúc syssetup

## Nguyên tắc

- Go điều phối; Bash thay đổi hệ thống.
- Profile được nhóm theo `profiles/<system>/`; profile mô tả feature cần chạy và
  `base-server.json` của từng hệ thống có thể lưu danh sách đích SSH cùng credential.
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

Các chế độ SSH command, script và workflow dùng chung một nguồn server. Người vận
hành có thể nhập một credential dùng chung cho danh sách host, hoặc nạp danh sách
`remoteServers` (mỗi host có credential riêng) từ `base-server` cùng hệ thống.

Dependency luôn chạy trước feature phụ thuộc và chỉ xuất hiện một lần trong execution plan.

## Ranh giới package Go

- `domain`: model không phụ thuộc infrastructure.
- `config`: đọc profile.
- `registry`: khám phá feature, validation và dependency resolution.
- `platform`: nhận diện `/etc/os-release` và quyền root.
- `runner`: timeout, process execution, output và log.
- `report`: chuẩn hóa kết quả và sinh report Markdown, HTML tự chứa hoặc workbook Excel sau mỗi
  lần thực thi.
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
feature `remoteOnly` hoặc feature local đã chủ động bật `workflowCompatible`, và
chỉ được phụ thuộc step đã khai báo trước.

Trước khi chạy, mỗi step tự tìm profile cùng hệ thống có cấu hình feature tương
ứng, validate parameter và điền default bằng feature registry. Parameter từ
profile được chuyển thành argument `key=value`; profile là nguồn cấu hình duy
nhất và ghi đè parameter cũ còn tồn tại trong workflow config.

`syssetup/workflow-config/v1` chỉ cần định danh workflow. Nó vẫn có thể chứa
invocation/args cho đối số vị trí, nhiều invocation hoặc artifact đặc thù mà
profile không biểu diễn. `deriveArgs` cho phép step sau thu thập argument theo
prefix hoặc vị trí từ config của step trước
mà không lặp lại dữ liệu, ví dụ lấy argument đầu tiên (tên interface) của
`create-vlan` cho `verify-network`. `argumentIndex` sử dụng chỉ số bắt đầu từ `0`.
`failurePolicy` hỗ trợ `stop-server` (mặc định) và `continue`; chính sách `continue`
vẫn đánh dấu server thất bại nhưng tiếp tục chạy invocation và step kế tiếp.
`executionMode` mặc định là `remote`; giá trị `local` chạy tuần tự ngay trên máy
đang chạy `syssetup`, không yêu cầu danh sách server hay thông tin SSH và chỉ cho
phép feature đã bật `workflowCompatible`.

Mỗi invocation có thể khai báo `artifacts` gồm `id` và local `source`. Với remote
workflow, executor mã hóa nội dung vào stdin SSH, tạo file tạm với `umask 077`,
export đường dẫn qua biến `SYSSETUP_ARTIFACT_<ID>` rồi xóa bằng `trap`. Với local
workflow, biến này trỏ thẳng đến đường dẫn tuyệt đối của file nguồn. Cả hai chế
độ đều kiểm tra file và giới hạn kích thước trước khi chạy; đường dẫn tương đối
được resolve từ thư mục chứa workflow config.

## Mở rộng ngoài repo

Có thể đặt feature ở thư mục khác và chạy:

```bash
syssetup list --features-dir /opt/company/features
syssetup run --features-dir /opt/company/features --profile company-server.json
```

Nhờ đó feature nội bộ có thể nằm trong repo riêng mà không fork core tool. API version cho phép phát triển manifest v2 sau này mà vẫn phát hiện cấu hình không tương thích rõ ràng.
