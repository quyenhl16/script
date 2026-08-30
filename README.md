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

- `Features`: bảng feature được đánh số và sắp xếp alphabet theo ID, cùng trạng
  thái, version, quyền thực thi và panel chi tiết. Index vẫn giữ nguyên khi lọc.
- `Plan`: thứ tự thực thi sau khi resolve dependency.
- `Logs`: output cập nhật trong lúc runner hoạt động.
- `Remote SSH`: nhập danh sách server, user, password để chạy Linux command
  hoặc gửi một script cục bộ lên nhiều server qua SSH.
- `Workflows`: danh sách quy trình nhiều step; mỗi server chạy tuần tự theo
  dependency trong khi các server vẫn được xử lý song song.
- `Profiles`: khám phá toàn bộ `profiles/*.json`; chọn profile bằng `Enter` để
  thay selection và parameters ngay trong TUI mà không cần khởi động lại.
- Layout tự thích nghi; panel chi tiết được ẩn trên terminal hẹp.

Phím tắt:

| Phím | Chức năng |
|---|---|
| `j/k`, `↑/↓` | Di chuyển |
| `Space`, `Enter` | Chọn hoặc bỏ chọn feature |
| `/` | Lọc feature |
| `a`, `n` | Chọn tất cả feature đang hiển thị hoặc bỏ chọn tất cả |
| `1/2/3/4/5/6`, `Tab` | Chuyển Features, Plan, Logs, Remote SSH, Workflows và Profiles |
| `r` | Chạy execution plan |
| `c` | Hủy plan đang chạy |
| `F2` | Đổi giữa command, script và workflow trong Remote SSH |
| `F5` | Chạy SSH trên tất cả server đã nhập |
| `?` | Hiện trợ giúp |
| `q` | Thoát |

Nhấn `6` để mở danh sách profile, dùng `j/k` hoặc phím mũi tên để chọn rồi
nhấn `Enter`. TUI sẽ xóa selection/parameters của profile cũ, nạp profile mới và
quay về tab `Features`. Profile khởi động vẫn có thể chọn bằng `--profile`; dùng
`--profiles-dir` nếu các profile có thể chọn nằm ngoài thư mục `profiles/`.

Trong tab `Remote SSH`, danh sách server dùng dấu phẩy hoặc khoảng trắng,
ví dụ `10.0.0.10, 10.0.0.11:2222`. Nếu không ghi port thì mặc định là `22`.
Các server chạy song song với timeout 30 giây/server và kết quả được hiển thị
riêng. Password chỉ được giữ trong bộ nhớ; host key được lưu theo cơ chế
trust-on-first-use tại thư mục cấu hình người dùng và bị từ chối nếu thay đổi ở
lần kết nối sau.

### Workflow Prepare Setup Deploy

Workflow `prepare-setup-deploy` chạy theo thứ tự trên từng server:

```text
1.1 create-local-path
1.2 create-bond-vlan  after create-local-path
1.3 verify-network    after create-vlan
```

Mở tab `5 Workflows`, chọn workflow và nhấn `Enter`. Dashboard sẽ nạp file
`workflow-configs/prepare-setup-deploy.json`; nhập server/user/password rồi nhấn
`F5`. Có thể thêm nhiều invocation trong `create-vlan` để tạo nhiều VLAN. Bước
`verify-network` không cần cấu hình riêng: executor tự lấy và loại trùng các giá
trị `gateway=` của toàn bộ invocation VLAN, sau đó ping các gateway đó.

Nếu một step lỗi trên server nào, các step sau chỉ bị bỏ qua trên server đó;
những server khác vẫn tiếp tục. Kết quả hiển thị theo server, step và invocation.

### Tạo Bond VLAN qua SSH

Feature `create-bond-vlan` được đóng gói sẵn để tạo VLAN trên
bond interface của các server RHEL-compatible:

1. Tìm `create-bond-vlan` trong tab `Features` và nhấn `Enter`; giao diện sẽ mở
   `Remote SSH` và nạp script tương ứng.
2. Nhập server, tài khoản `root`, password và trường `Args`, ví dụ:
   `bond2.306 ip=10.0.36.87 prefix=24 gateway=10.0.36.254`.
3. Nhấn `F5` để chạy đồng thời trên các server.

Script kiểm tra interface/VLAN/IPv4/prefix, yêu cầu `ip` đi cùng `prefix`, sao
lưu file cấu hình cũ trước khi thay đổi và chỉ chấp nhận VLAN ID từ 1 đến 4094.
Mỗi argument được quote riêng trước khi gửi qua SSH; script chạy bằng `bash` và
yêu cầu SSH trực tiếp bằng tài khoản `root`.

### Tạo danh sách thư mục qua SSH

Feature `create-local-path` tạo một hoặc nhiều thư mục trên tất cả server đã
nhập. Chọn feature trong tab `Features`, nhập thông tin SSH rồi cấu hình `Args`:

```text
/data/app /data/log /opt/company/cache mode=0755 owner=root group=root
```

Đường dẫn phải là đường dẫn tuyệt đối và không chứa khoảng trắng. Các tùy chọn
`mode`, `owner`, `group` áp dụng cho mọi thư mục trong danh sách; giá trị mặc
định lần lượt là `0755`, `root`, `root`. Script từ chối `/`, thành phần `.`/`..`,
đường dẫn đang là file và user/group không tồn tại.

### Load XML config vào NetConf pod master

Feature `load-netconf-config` chạy local bằng `kubectl`; không dùng SSH và không
nằm trong workflow. Feature kiểm tra lần lượt `netconf-0` và `netconf-1` trong
namespace đã cấu hình bằng lệnh `show confd-state ha`, sau đó chỉ thực hiện load
trên pod duy nhất trả về `confd-state ha mode master`. Nếu không có master hoặc
cả hai pod cùng báo master, feature dừng mà không load cấu hình.

Sửa namespace và các đường dẫn nếu cần trong
`profiles/load-netconf-config.json`:

```json
{
  "id": "load-netconf-config",
  "parameters": {
    "namespace": "your-namespace",
    "container": "",
    "confd_dir": ".",
    "source_config_file": "/path/to/config.xml",
    "destination_config_file": "config.xml"
  }
}
```

Sau đó chạy:

```bash
./bin/syssetup plan --profile profiles/load-netconf-config.json
./bin/syssetup run --profile profiles/load-netconf-config.json
```

Sau khi xác định master, feature copy file local `source_config_file` qua stdin
của `kubectl exec` vào file tạm, rồi đổi tên nguyên tử thành
`destination_config_file` trong pod master. Pod slave không nhận file. Feature
sau đó mở `./run_cli.sh` ở chế độ non-interactive, chạy `config`, `load merge`
với tên file trong `destination_config_file`, rồi `commit`. CLI dừng ngay khi gặp lỗi. Nếu transaction
load/commit lần đầu thất bại, toàn bộ transaction được chạy lại một lần; sau hai
lần lỗi feature trả trạng thái failed.

Máy chạy `syssetup` cần có `kubectl` và quyền `get pod`, `pods/exec` trong
namespace. Nếu pod có nhiều container, đặt `container` thành tên container chạy
ConfD. `confd_dir` mặc định là working directory của container; nếu `run_cli.sh`
nằm ở chỗ khác, đặt tham số này thành đường dẫn thư mục đó.

### Kiểm tra XML config local

Feature `verify-xml-config` chỉ đọc một file XML local và so sánh các path quan
trọng với bộ rule JSON. Feature này chạy qua luồng local `Features -> Plan -> Run`,
không dùng SSH và không nằm trong workflow.

Sửa `xml_file` trong profile mẫu `profiles/verify-xml-config.json` thành file XML
cần kiểm tra. `rules_file` mặc định trỏ tới
`checks/xml/system-critical-paths.json` và có thể đổi sang file rule khác:

```json
{
  "id": "verify-xml-config",
  "parameters": {
    "xml_file": "/opt/company/conf/config_amf.xml",
    "rules_file": "checks/xml/system-critical-paths.json"
  }
}
```

Sau đó chạy:

```bash
./bin/syssetup plan --profile profiles/verify-xml-config.json
./bin/syssetup run --profile profiles/verify-xml-config.json
```

Rule so sánh scalar khai báo XPath và giá trị mong đợi:

```json
{
  "apiVersion": "syssetup/xml-check/v1",
  "checks": [
    {
      "id": "application-data-path",
      "xpath": "/system/paths/application",
      "expected": "/data/app",
      "compare": "exact",
      "required": true,
      "sensitive": false
    }
  ]
}
```

Với container hoặc list chỉ cần xác nhận tồn tại và xem toàn bộ giá trị hiện
tại, dùng `reportOnly: true` và không khai báo `expected`:

```json
{
  "id": "amf-support-tai",
  "xpath": "/*[local-name()='config']/*[local-name()='amf']/*[local-name()='sctp_handler_service']/*[local-name()='sctp_conf']/*[local-name()='support_tai']",
  "reportOnly": true,
  "required": true
}
```

Nếu XPath trên chọn nhiều `support_tai`, output hiển thị số node và từng leaf:

```text
[PASS] amf-support-tai: matched=9
  [1]
    no='1'
    plmn_id_mcc='452'
    plmn_id_mnc='04'
    tac='000001'
```

`compare` hỗ trợ `exact`, `trimmed`, `integer`, `boolean`, `ip` và `regex`.
Rule so sánh phải chọn đúng một node; rule `reportOnly` cho phép nhiều node.
Node không tồn tại sẽ là lỗi nếu `required` là `true`, ngược lại được ghi
`[SKIP]`. Đặt `sensitive: true` để không in value mong đợi và value thực tế ra
log. Với XML có namespace, XPath có thể dùng `local-name()` như các rule AMF
trong `checks/xml/system-critical-paths.json`.

Máy chạy `syssetup` cần có `python3` và `xmllint` (gói `libxml2`). Cả hai đường
dẫn có thể là tuyệt đối hoặc tương đối với thư mục hiện tại. Feature chỉ đọc và
kiểm tra nội dung, không sửa file XML.

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
`workflows/`, `workflow-configs/`, `checks/`,
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
