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
./bin/syssetup plan --profile profiles/01HTX/base-server.json
./bin/syssetup run --profile profiles/01HTX/base-server.json --dry-run
sudo ./bin/syssetup run --profile profiles/01HTX/base-server.json
sudo ./bin/syssetup tui --profile profiles/01HTX/base-server.json
```

Kiểm tra VIP, OSPF và kết nối mạng của workload Kubernetes `pramf01`:

```bash
./bin/syssetup plan --profile profiles/01HTX/k8s-connectivity-check.json
./bin/syssetup run --profile profiles/01HTX/k8s-connectivity-check.json
```

Đổi `phase` trong profile thành `vip`, `ospf`, `ping`, `curl` hoặc `all` để chọn
phạm vi kiểm tra. Namespace, VIP và địa chỉ NF đích đều có thể cấu hình trong
`parameters` của feature `k8s-connectivity-check`.

Trong phase `ping`, các tham số `ip_ausf`, `ip_udm`, `ip_smf`,
`ip_amf_remote`, `ip_nrf`, `ip_eir`, `ip_nssf` và `ip_gnodeb` chấp nhận một hoặc
nhiều IPv4, phân cách bằng dấu phẩy hoặc khoảng trắng. Mỗi MM pod sẽ ping từng
địa chỉ NF; mỗi IPGW pod sẽ ping từng địa chỉ gNodeB. Cấu hình một IP cũ vẫn được
hỗ trợ:

```json
{
  "ip_ausf": "192.0.2.10, 192.0.2.11",
  "ip_udm": "192.0.2.20 192.0.2.21",
  "ip_amf_self": "192.0.2.34",
  "ip_eir": "192.0.2.41",
  "ip_nssf": "",
  "ip_gnodeb": "198.51.100.10, 198.51.100.11"
}
```

Phase `curl` chạy từ từng MM pod đến các URL của AUSF, UDM, SMF, AMF remote,
NRF, EIR và NSSF. Mỗi tham số `curl_*_urls` nhận một hoặc nhiều URL HTTP(S), cho
phép khai báo port và path riêng cho từng NF:

```json
{
  "phase": "curl",
  "curl_ausf_urls": "http://192.0.2.10:8080/health http://192.0.2.11:8080/health",
  "curl_udm_urls": "https://192.0.2.20:8443/health",
  "curl_smf_urls": "http://192.0.2.30:8080/",
  "curl_amf_self_urls": "http://192.0.2.34:8080/health",
  "curl_amf_remote_urls": "http://192.0.2.35:8080/health",
  "curl_nrf_urls": "http://192.0.2.40:8080/nnrf-nfm/v1/nf-instances",
  "curl_eir_urls": "http://192.0.2.41/",
  "curl_nssf_urls": "",
  "curl_connect_timeout_seconds": 3,
  "curl_max_time_seconds": 10,
  "curl_insecure": false
}
```

Bất kỳ HTTP status từ `100` đến `599` đều được coi là kết nối thành công vì
phase này kiểm tra DNS/TCP/TLS/HTTP, không kiểm tra nghiệp vụ của API. Lỗi timeout,
connection refused, DNS hoặc TLS sẽ báo FAIL. Với chứng thư nội bộ chưa được tin
cậy, có thể đặt `curl_insecure` thành `true`.

Query NRF discovery trực tiếp từ host, kiểm tra AMF/SMF instance và in response
JSON dạng dễ đọc:

```bash
./bin/syssetup plan --profile profiles/01HTX/nrf-query-check.json
./bin/syssetup run --profile profiles/01HTX/nrf-query-check.json
```

Feature `nrf-query-check` yêu cầu NRF trả HTTP `2xx`, body là JSON có mảng
`nfInstances` và ít nhất một instance đúng `nfType`. Mặc định instance phải có
`nfStatus=REGISTERED`; đặt `require_registered=false` nếu chỉ cần kiểm tra kết quả
discovery. Hai URL đầy đủ được cấu hình bằng `amf_query_url` và `smf_query_url`.

Kiểm tra danh sách service bắt buộc của namespace `pramf01`, ready endpoint,
External IP kỳ vọng và kết nối đến mọi cổng TCP qua ClusterIP (hoặc endpoint IP
đối với headless service):

```bash
./bin/syssetup plan --profile profiles/01HTX/k8s-service-check.json
./bin/syssetup run --profile profiles/01HTX/k8s-service-check.json
```

Feature `k8s-service-check` dùng `curl` với giao thức telnet để chỉ kiểm tra bắt
tay TCP, không yêu cầu dịch vụ phải chạy HTTP. Nếu không có `curl`, feature dùng
`telnet` kết hợp `timeout`. Mặc định `probe_targets` là `all`, nên cả ClusterIP và
External IP đều được kiểm tra; đặt thành `cluster` hoặc `external` để giới hạn
phạm vi. Mọi service bắt buộc phải có ít nhất một địa chỉ ready trong resource
Endpoints; headless service không có IP ảo được TCP probe qua từng endpoint IP.
Kiểm tra endpoint và External IP vẫn chạy khi `tcp_probe=false`.

Tham số `external_ip_expectations` chỉ định các service cần kiểm tra External IP
theo cú pháp `service=ip[,ip];service=ip`. Tập IP thực tế phải khớp chính xác,
không phụ thuộc thứ tự:

```json
{
  "external_ip_expectations": "comm-sbi=192.168.5.90;nm-loadbalancer-svc=192.168.5.90,68.240.20.130"
}
```

UDP, SCTP và service không khai báo TCP port vẫn được kiểm tra sự tồn tại,
endpoint và External IP nếu có cấu hình. Tham số `services` nhận tên phân cách
bằng dấu phẩy hoặc khoảng trắng.

Kiểm tra environment của Deployment, StatefulSet và DaemonSet theo ma trận Excel:

```bash
cp /path/to/pramf01-input_100K.xlsx .
./bin/syssetup plan --profile profiles/01HTX/k8s-env-check.json
./bin/syssetup run --profile profiles/01HTX/k8s-env-check.json
```

Feature `k8s-env-check` đọc sheet `VDU`: cột B chứa thuộc tính bắt đầu bằng
`environments_`, cột C chứa tên ENV Kubernetes chính xác, và các cột từ D trở đi
chứa giá trị kỳ vọng của từng component. Chỉ những dòng đã khai tên ENV trong
cột C mới được so sánh; dòng có cột C trống được bỏ qua dù các cột service có dữ
liệu. Ô service trống nghĩa là ENV không áp dụng cho component đó;
`<PRESENT>` chỉ kiểm tra sự tồn tại, `<EMPTY>` yêu cầu giá trị rỗng,
`<REGEX:...>` kiểm tra bằng biểu thức chính quy, còn
`<SECRET:name/key>`/`<CONFIGMAP:name/key>` kiểm tra nguồn tham chiếu mà không ghi
giá trị nhạy cảm ra output.

Khi giá trị không khớp, kết quả hiển thị cả `expected`, `actual` và ô Excel nguồn.
Giá trị lấy từ Kubernetes Secret vẫn được che và chỉ hiển thị tên Secret/key.

Tên component được tự đổi `_` thành `-` và đối chiếu với workload/container. Nếu
không thể suy luận duy nhất, cấu hình `mapping_file` theo mẫu
`checks/k8s-env/workload-map.example.json`. Feature resolve cả `env`, `envFrom`,
ConfigMap và Secret từ Pod template. Giá trị Secret chỉ tồn tại trong bộ nhớ và
không được in ra report. `extra_policy` nhận `warn`, `fail` hoặc `ignore`; có thể
dùng `ignore_extra` cho các glob như `POD_*`.

Các ô công thức Excel sử dụng cached value đã lưu trong workbook. Hãy recalculate
và save file bằng Excel trước khi chạy; công thức không có cached value sẽ bị từ
chối để tránh so sánh dữ liệu cũ hoặc rỗng.

Kiểm tra CPU và memory request/limit của workload theo cùng file Excel:

```bash
./bin/syssetup plan --profile profiles/01HTX/k8s-resource-check.json
./bin/syssetup run --profile profiles/01HTX/k8s-resource-check.json
```

Feature `k8s-resource-check` đọc các thuộc tính `mem_size`, `num_cpus`,
`mem_size_limit`, `num_cpus_limit`, `init_resources_cpu` và
`init_resources_mem` trong cột B. Giá trị số trần mặc định được hiểu là `Mi` cho
memory và `m` cho CPU; các Kubernetes quantity tương đương như `4096Mi`/`4Gi`
hay `2000m`/`2` được coi là bằng nhau. Resource chính được đối chiếu với
`containers[].resources`; resource init chỉ được kiểm tra khi Excel có giá trị
`init_resources_*`.

Dòng `pv_storage` là tùy chọn. Với mỗi service có khai giá trị ở dòng này,
feature tìm PVC được workload tham chiếu, theo `spec.template.spec.volumes` hoặc
`volumeClaimTemplates` của StatefulSet, rồi kiểm tra `spec.capacity.storage` của
từng PV đã bind. Tất cả PV của các replica phải có dung lượng đúng với Excel;
PVC chưa bind, PV không tồn tại hoặc một PV sai dung lượng đều làm kết quả FAIL.
Tài khoản chạy `kubectl` cần quyền đọc PVC trong namespace và đọc PV ở cluster.

Tên workload/container được tự suy luận như `k8s-env-check`. Với workload có
nhiều container hoặc init container, dùng `mapping_file` theo mẫu
`checks/k8s-resource/workload-map.example.json`. `extra_policy` nhận `warn`,
`fail` hoặc `ignore`; `ignore_extra` nhận glob của đường dẫn resource, ví dụ
`container.requests.hugepages-*`.
Khi `show_pass=true`, mỗi resource khớp vẫn hiển thị riêng giá trị `expected`
từ Excel và `actual` từ Kubernetes, kể cả khi hai quantity chỉ tương đương sau
chuẩn hóa như `2000m` và `2`.

Hoặc:

```bash
chmod +x setup.sh
sudo ./setup.sh tui
```

### Workflow kiểm tra sau triển khai

Workflow `post-deployment-validation` chạy tuần tự trên máy local sau khi hệ
thống Kubernetes đã lên:

```text
1. k8s-service-check      kiểm tra service Kubernetes
2. k8s-resource-check     sau service-check
3. k8s-env-check          sau resource-check
4. k8s-connectivity-check sau env-check
5. compare-xml-config     sau connectivity-check
```

Trước khi chạy riêng feature hoặc chạy workflow, đặt file Excel baseline tại
đường dẫn `input_file` đã khai trong profile. Với profile mẫu, chạy lệnh sau từ
thư mục gốc dự án:

```bash
cp /path/to/pramf01-input_100K.xlsx pramf01-input_100K.xlsx
```

Đồng thời cấu hình `source_xml` và `target_xml` trong
`profiles/01HTX/compare-xml-config.json` tới hai file XML cần so sánh. Step
`compare-xml-config` sử dụng `checks/xml/system-critical-paths.json` theo mặc định.

Mở tab `5 Workflows`, chọn `post-deployment-validation` rồi nhấn `F5`; workflow
chạy local và không yêu cầu server, user hoặc password SSH. Máy đang chạy
`syssetup` cần truy cập được namespace `pramf01` bằng `kubectl`, đồng thời có
`python3`, `curl` hoặc `telnet` theo loại kiểm tra. Nếu một bước lỗi, workflow vẫn
ghi nhận lỗi và tiếp tục chạy các bước còn lại. Output `stdout`/`stderr` của step
đang chạy được cập nhật trực tiếp trong khung `Local workflow results`.

Mỗi step lấy parameter từ profile cùng hệ thống có chứa feature tương ứng (ưu
tiên profile có tên trùng feature ID). Vì vậy chỉ cần sửa profile feature; không
cần lặp lại `key=value` trong `workflow-configs/post-deployment-validation.json`.

### AMF system verification guide

Profile `profiles/01HTX/amf-system-check-guide.json` chạy feature chỉ-đọc
`amf-system-check-guide`. Guide song ngữ Anh-Việt được hiển thị trong tab `Logs`,
gồm ba phần: trước khi cài AMF CNF, sau khi cài AMF CNF và thông tin bổ sung.

## TUI dashboard

TUI chạy toàn màn hình theo phong cách dashboard quản trị như k9s:

- `Features`: bảng feature được đánh số và sắp xếp alphabet theo ID, cùng trạng
  thái, version, quyền thực thi và panel chi tiết. Panel liệt kê parameter
  required/optional, kiểu dữ liệu, mô tả, default và giá trị từ profile; parameter
  bắt buộc còn thiếu được đánh dấu `MISSING`. Index vẫn giữ nguyên khi lọc.
- `Plan`: thứ tự thực thi sau khi resolve dependency.
- `Logs`: output cập nhật trong lúc runner hoạt động.
- `Remote SSH`: nhập danh sách server, user, password để chạy Linux command
  hoặc gửi một script cục bộ lên nhiều server qua SSH.
- `Workflows`: danh sách quy trình nhiều step; hỗ trợ chạy local hoặc tuần tự
  trên từng server remote theo dependency.
- `Profiles`: khám phá đệ quy `profiles/<system>/*.json`, hiển thị cột hệ thống;
  chọn profile bằng `Enter` để
  thay selection và parameters ngay trong TUI mà không cần khởi động lại.
- Layout tự thích nghi; panel chi tiết được ẩn trên terminal hẹp.

Phím tắt:

| Phím | Chức năng |
|---|---|
| `j/k`, `↑/↓` | Di chuyển |
| `Space`, `Enter` | Chọn hoặc bỏ chọn feature |
| `/` | Lọc feature |
| `a`, `n` | Chọn tất cả feature đang hiển thị hoặc bỏ chọn tất cả |
| `PgUp`, `PgDn` | Cuộn nội dung parameter trong panel chi tiết feature |
| `1/2/3/4/5/6`, `Tab` | Chuyển Features, Plan, Logs, Remote SSH, Workflows và Profiles |
| `r` | Chạy execution plan |
| `c` | Hủy plan đang chạy |
| `F2` | Đổi giữa command, script và workflow trong Remote SSH |
| `F3` | Nạp toàn bộ server và credential từ `base-server` của hệ thống hiện tại |
| `F4` | Chuyển định dạng report giữa Markdown, HTML và Excel |
| `F5` | Chạy SSH trên tất cả server đã nhập hoặc đã nạp |
| `F6` | Bật hoặc tắt web server xem HTML report |
| `?` | Hiện trợ giúp |
| `q` | Thoát |

Nhấn `6` để mở danh sách profile, dùng `j/k` hoặc phím mũi tên để chọn rồi
nhấn `Enter`. TUI sẽ xóa selection/parameters của profile cũ, nạp profile mới và
quay về tab `Features`. Profile khởi động vẫn có thể chọn bằng `--profile`; dùng
`--profiles-dir` nếu các profile có thể chọn nằm ngoài thư mục `profiles/`.

Trong tab `Remote SSH`, danh sách server dùng dấu phẩy hoặc khoảng trắng,
ví dụ `10.0.0.10, 10.0.0.11:2222`. Nếu không ghi port thì mặc định là `22`.
Các server chạy song song với timeout 30 giây/server và kết quả được hiển thị
riêng. Password nhập thủ công chỉ được giữ trong bộ nhớ và mọi password đều không
được ghi vào log; host key được lưu theo cơ chế
trust-on-first-use tại thư mục cấu hình người dùng và bị từ chối nếu thay đổi ở
lần kết nối sau.

Profile được nhóm theo hệ thống. Mỗi thư mục con trực tiếp dưới `profiles/` là một
hệ thống và chứa `base-server.json` cùng các profile feature của hệ thống đó:

```text
profiles/
└── 01HTX/
    ├── base-server.json
    ├── compare-xml-config.json
    ├── k8s-connectivity-check.json
    ├── update-alarm-mappings.json
    └── verify-xml-config.json
```

`base-server.json` lưu riêng credential cho từng máy (hãy thay các giá trị mẫu):

```json
{
  "remoteServers": [
    {
      "name": "server-01",
      "ip": "10.0.0.10",
      "port": 22,
      "username": "root",
      "password": "change-me"
    }
  ]
}
```

Vì password nằm dạng rõ trong JSON, cần giới hạn quyền đọc file/thư mục profile
cho đúng tài khoản vận hành và không commit credential thật vào kho mã nguồn.

Trong mọi chế độ SSH (command, script và workflow), có thể nhập server/user/password
thủ công như trước. Nếu không nhập thủ công, nhấn `F3` để nạp toàn bộ server kèm
credential từ profile `base-server` thuộc cùng hệ thống; sau đó nhấn `F5` để chạy.
Nếu nhập server thủ công sau khi nạp, dữ liệu thủ công được ưu tiên.

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
`verify-network` không cần cấu hình riêng: executor tự lấy và loại trùng tên
interface (argument đầu tiên) của toàn bộ invocation VLAN. Feature sau đó kiểm tra
từng interface đã tồn tại và có trạng thái `UP`; không ping gateway.

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

### Enable SCTP trên RHEL qua SSH

Feature `enable-sctp` cài và kích hoạt SCTP trên các server Red Hat Enterprise
Linux 8 trở lên:

1. Trong tab `Features`, tìm `enable-sctp` và nhấn `Enter` để mở script trong
   `Remote SSH`.
2. Nạp server và credential từ profile `base-server` bằng `F3`, hoặc nhập thủ
   công. Tài khoản SSH phải là `root`; trường `Args` để trống.
3. Nhấn `F5` để chạy song song trên tất cả server.

Script cài gói `kernel-modules-extra-$(uname -r)`, ghi `sctp` vào
`/etc/modules-load.d/sctp.conf`, comment dòng blacklist cuối nếu dòng đó còn hoạt
động, chạy `modprobe sctp` và xác nhận module xuất hiện trong `lsmod`. Script từ
chối hệ điều hành không phải RHEL hoặc RHEL cũ hơn phiên bản 8.

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
`profiles/01HTX/load-netconf-config.json`:

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
./bin/syssetup plan --profile profiles/01HTX/load-netconf-config.json
./bin/syssetup run --profile profiles/01HTX/load-netconf-config.json
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

### Export XML config từ NetConf pod master

Feature `export-netconf-config` là chiều ngược lại của `load-netconf-config`. Nó
xác định duy nhất pod `netconf-0` hoặc `netconf-1` đang có ConfD HA mode
`master`, mở `run_cli.sh` và chạy:

```text
show running-config amf | display xml | save amf-running-config.xml
```

Cấu hình feature tại `profiles/01HTX/export-netconf-config.json`:

```json
{
  "id": "export-netconf-config",
  "parameters": {
    "namespace": "your-namespace",
    "container": "",
    "confd_dir": ".",
    "remote_config_file": "amf-running-config.xml",
    "destination_config_file": "amf-running-config.xml",
    "overwrite": false
  }
}
```

Sau khi CLI tạo file trong pod master, feature truyền trực tiếp nội dung file về
`destination_config_file` qua `kubectl exec`, kiểm tra file không rỗng và có nội
dung XML, đặt quyền local thành `0600`, rồi xóa file tạm trong pod. Feature mặc
định từ chối ghi đè; đặt `overwrite=true` nếu muốn thay thế file local đã tồn tại.
Cách truyền trực tiếp không phụ thuộc vào `tar` và tránh lỗi
`tar contents corrupted` của `kubectl cp` trên một số minimal container image.

```bash
./bin/syssetup plan --profile profiles/01HTX/export-netconf-config.json
./bin/syssetup run --profile profiles/01HTX/export-netconf-config.json
```

### Kiểm tra XML config local

Feature `verify-xml-config` chỉ đọc một file XML local và so sánh các path quan
trọng với bộ rule JSON. Feature này chạy qua luồng local `Features -> Plan -> Run`,
không dùng SSH và không nằm trong workflow.

Sửa `xml_file` trong profile mẫu `profiles/01HTX/verify-xml-config.json` thành file XML
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
./bin/syssetup plan --profile profiles/01HTX/verify-xml-config.json
./bin/syssetup run --profile profiles/01HTX/verify-xml-config.json
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

Feature `verify-xml-config` cần Python 3.6 trở lên (`python3`) và `xmllint` (gói
`libxml2`). Các đường
dẫn có thể là tuyệt đối hoặc tương đối với thư mục hiện tại. Feature chỉ đọc và
kiểm tra nội dung, không sửa file XML.

### So sánh hai XML config

Feature `compare-xml-config` so sánh hai file XML theo toàn bộ leaf path, bỏ qua
khác biệt về indentation và namespace prefix. Với list lặp, feature ưu tiên các
leaf key `id`, `name`, `no`, `key`; nếu không có key ổn định mới dùng chỉ số
`[1]`, `[2]`. Vì vậy việc đổi thứ tự list có key không tạo ra hàng loạt sai khác giả.

Sửa hai đường dẫn trong `profiles/01HTX/compare-xml-config.json` rồi chạy:

```bash
./bin/syssetup run --profile profiles/01HTX/compare-xml-config.json
```

Các tham số:

```json
{
  "id": "compare-xml-config",
  "parameters": {
    "source_xml": "/opt/company/conf/config-old.xml",
    "target_xml": "/opt/company/conf/config-new.xml",
    "rules_file": "checks/xml/system-critical-paths.json",
    "show_equal": false
  }
}
```

`total_paths` là hợp của path trong hai file. `different` gồm path đổi giá trị,
chỉ có ở source hoặc chỉ có ở target. Mặc định output hiển thị mọi sai khác và
mọi path critical; đặt `show_equal: true` để hiển thị cả path thường giống nhau.
Các path nằm dưới XPath khai báo trong `system-critical-paths.json` có nhãn
`[CRITICAL:<id>]` nổi bật:

```text
[FAIL] Summary: total_paths=445 same=440 different=5 changed=3 only_source=1 only_target=1
[FAIL] Critical summary: rules=7 matched_rules=7 total_paths=74 same=72 different=2
[FAIL] [CRITICAL:amf-default-nrf-uri] [CHANGED] /config/amf/mm_service/nf_conf/default_nrf_uri
  source='http://10.0.0.1:20000'
  target='http://10.0.0.2:20000'
```

Nếu có sai khác hoặc thiếu một critical rule bắt buộc, feature trả trạng thái
failed và report Markdown/HTML/Excel vẫn được tạo đầy đủ. Feature chỉ cần `python3`,
không yêu cầu `xmllint`.

### Cập nhật AMF alarm mappings

Feature `update-alarm-mappings` gửi tuần tự các payload alarm qua HTTP `PUT`.
15 nhóm payload từ `sctp` đến `dns` được lưu tại
`features/update-alarm-mappings/alarm-mappings.json`, tách khỏi script để có thể
sửa hoặc bổ sung mà không phải tạo lại lệnh shell.

Kiểm tra `endpoint` trong profile trước khi chạy:

```bash
./bin/syssetup run --profile profiles/01HTX/update-alarm-mappings.json
```

Profile hỗ trợ các tham số:

```json
{
  "id": "update-alarm-mappings",
  "parameters": {
    "endpoint": "http://10.0.1.101:33334/nnm-service/v1/updatealarm/AMF",
    "requests_file": "/opt/company/conf/alarm-mappings.json",
    "connect_timeout_seconds": 5,
    "request_timeout_seconds": 30
  }
}
```

`requests_file` là tùy chọn; nếu bỏ qua, feature dùng file đi kèm nói trên.
Mỗi request hiển thị tên pod, HTTP status và response body. Feature tiếp tục gửi
các request còn lại khi một request lỗi, sau đó in tổng số `passed`/`failed` và
trả trạng thái failed nếu có bất kỳ lỗi curl hoặc HTTP ngoài khoảng 2xx. Toàn bộ
kết quả cũng được lưu trong report Markdown/HTML/Excel của lần chạy.

Khi build từ source cần Go 1.24 trở lên. Binary đã build vẫn có thể chạy độc lập trên server đích.

Log mặc định được ghi vào `syssetup.log`. Có thể đổi bằng `--log /var/log/syssetup.log`.
Các dòng `[PASS]` được tô xanh và `[FAIL]` được tô đỏ trên CLI/TUI; file log
vẫn lưu text thuần. Đặt biến môi trường `NO_COLOR=1` để tắt màu hiển thị.

## Report kết quả

Mỗi lần thực thi feature, SSH command/script hoặc workflow sẽ tạo một report độc
lập ở định dạng Markdown, HTML hoặc Excel. Markdown là định dạng mặc định và có thể đọc trực tiếp trên server bằng
`less` hoặc `vim`:

```bash
less reports/md/01HTX/20260904_153025.000_prepare-setup-deploy.md
```

Report được tách theo định dạng rồi nhóm theo hệ thống:

```text
reports/
├── md/
│   └── <system>/*.md
├── html/
│   └── <system>/*.html
└── xlsx/
    └── <system>/*.xlsx
```

Mỗi file gồm trạng thái tổng, thời gian chạy, thống kê thành công/thất bại và
output chi tiết theo feature/server/step. Mã màu terminal được loại bỏ và
credential SSH không được đưa vào report.

Chọn định dạng khi chạy CLI:

```bash
syssetup run --profile profiles/01HTX/base-server.json --report-format md
syssetup run --profile profiles/01HTX/base-server.json --report-format html
syssetup run --profile profiles/01HTX/base-server.json --report-format xlsx
```

Dùng `--reports-dir PATH` để đổi thư mục lưu. Trong TUI, định dạng hiện tại nằm
ở góc phải header; nhấn `F4` để chuyển tuần tự giữa `MD`, `HTML` và `XLSX`. Sau khi lệnh
kết thúc, đường dẫn report được hiển thị trên thanh trạng thái.

Với workflow, report Excel có sheet `Summary` chứa trạng thái tổng thể, thông tin
system/profile, thời gian và số lượng checklist `PASS`, `FAIL`, `SKIP`, `WARN`. Mỗi lần
chạy feature trong workflow vẫn được ghi vào một sheet riêng. Nội dung của sheet được
chia thành các nhóm kiểm tra và bảng checklist gồm `Check`, `Status`, `Expected`,
`Actual`, `Source`, `Details`; các cột so sánh được điền khi output có dữ liệu tương ứng.
Những dòng không thể chuyển thành checklist được giữ ở phần `Additional messages`.
Tên sheet được chuẩn hóa theo giới hạn của Excel và không bị trùng.

### Web browser cho HTML report

Chạy web server tích hợp để xem danh sách toàn bộ report HTML:

```bash
./bin/syssetup reports serve
```

Mặc định server chỉ lắng nghe tại `127.0.0.1:8080` và đọc report từ
`reports/html/`. Trang chỉ mục hiển thị tổng số report, trạng thái, system,
profile, loại report, thời gian, kích thước; hỗ trợ tìm kiếm và lọc theo trạng thái.

Khi syssetup chạy trên server không có giao diện đồ họa, tạo SSH tunnel từ máy cá nhân:

```bash
ssh -L 8080:127.0.0.1:8080 user@IP_SERVER
```

Sau đó mở `http://127.0.0.1:8080/` trên trình duyệt của máy cá nhân. Nhấn `Ctrl+C`
để dừng web server. Có thể thay đổi địa chỉ và thư mục report:

```bash
./bin/syssetup reports serve --listen 127.0.0.1:9090 --reports-dir /var/lib/syssetup/reports
```

Biến môi trường tương ứng là `SYSSETUP_REPORT_LISTEN` và `SYSSETUP_REPORTS_DIR`.
Chỉ dùng `--listen 0.0.0.0:8080` trong mạng được kiểm soát vì web server không có
xác thực và report có thể chứa thông tin vận hành nội bộ.

Trong TUI, nhấn `F6` để bật hoặc tắt web server mà không làm gián đoạn feature hay
workflow đang chạy. Header hiển thị `Web: STARTING`, `Web: ON`, `Web: STOPPING`
hoặc `Web: OFF`; khi khởi động thành công, URL được hiển thị trên thanh trạng thái.
TUI dùng chung `--reports-dir` và có thể đổi địa chỉ listen khi khởi động:

```bash
./bin/syssetup tui --report-listen 127.0.0.1:9090
```

## Tích hợp feature và workflow mới

Registry tự khám phá các thư mục con trong `features/`, vì vậy một feature thông thường
không cần đăng ký trong code Go hoặc sửa TUI. Trước khi tích hợp, chọn cách chạy phù hợp:

| Nhu cầu | Cấu hình feature |
|---|---|
| Chạy từ tab `Features` hoặc CLI với profile | Không đặt `remoteOnly`, profile chọn feature |
| Chạy trong local workflow | `workflowCompatible: true`, không đặt `remoteOnly` |
| Chạy trong remote workflow | `remoteOnly: true` hoặc `workflowCompatible: true` |
| Chạy script SSH một lần, không cần quản lý như feature | Dùng chế độ `Script` trong tab `Remote SSH`; không cần tạo feature |

### 1. Tạo feature package

Mỗi feature có một thư mục riêng. Nếu logic chính viết bằng Python, vẫn dùng `run.sh`
làm entrypoint vì runner hiện tại gọi entrypoint bằng Bash:

```text
features/my-check/
├── feature.json       # bắt buộc: metadata, timeout và parameter
├── run.sh             # bắt buộc: adapter theo Feature API
├── check.py           # tùy chọn: logic Python
└── templates/         # tùy chọn: asset riêng của feature
```

Manifest mẫu:

```json
{
  "apiVersion": "syssetup/v1",
  "id": "my-check",
  "name": "My system check",
  "version": "1.0.0",
  "description": "Kiểm tra trạng thái của hệ thống",
  "entrypoint": "run.sh",
  "supportedOS": ["rhel", "centos", "rocky", "almalinux", "fedora"],
  "requireRoot": false,
  "workflowCompatible": true,
  "timeoutSeconds": 300,
  "parameters": {
    "namespace": {
      "type": "string",
      "description": "Kubernetes namespace",
      "required": true
    },
    "show_pass": {
      "type": "boolean",
      "description": "Hiển thị các kết quả thành công",
      "default": true
    }
  }
}
```

`id` phải duy nhất. `entrypoint` phải nằm trong thư mục feature; `timeoutSeconds` phải
lớn hơn `0`. Parameter chỉ hỗ trợ `string`, `integer` và `boolean`. Với cách chạy qua
profile, tool chuẩn hóa parameter thành biến môi trường:

```text
namespace  -> SYSSETUP_PARAM_NAMESPACE
show_pass  -> SYSSETUP_PARAM_SHOW_PASS
```

### 2. Viết entrypoint theo Feature API

Entrypoint phải nhận action ở argument đầu tiên:

- `check`: trả `0` nếu trạng thái đã đúng, `10` nếu cần chạy `apply`; mã khác báo lỗi.
- `apply`: thực hiện thay đổi và phải có khả năng chạy lại an toàn.
- `verify`: xác nhận trạng thái sau thay đổi; trả mã khác `0` nếu kiểm tra thất bại.
- `rollback`: hoàn tác nếu feature hỗ trợ. Runner chưa tự động gọi action này.

Adapter mẫu cho một feature chỉ kiểm tra, không thay đổi hệ thống:

```bash
#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"
namespace="${SYSSETUP_PARAM_NAMESPACE:-default}"
show_pass="${SYSSETUP_PARAM_SHOW_PASS:-true}"

# Workflow truyền parameter dưới dạng key=value sau action; profile truyền qua env.
for argument in "${@:2}"; do
  case "$argument" in
    namespace=*) namespace="${argument#*=}" ;;
    show_pass=*) show_pass="${argument#*=}" ;;
    *) printf 'Unknown argument: %q\n' "$argument" >&2; exit 2 ;;
  esac
done

case "$action" in
  check)
    command -v python3 >/dev/null 2>&1 || {
      printf '[FAIL] python3 command was not found\n' >&2
      exit 1
    }
    # Audit phải chạy ở mọi lần thực thi.
    exit 10
    ;;
  apply)
    # Feature read-only nên không thay đổi trạng thái.
    :
    ;;
  verify)
    python3 "$(dirname "$0")/check.py" \
      --namespace "$namespace" \
      --show-pass "$show_pass"
    ;;
  rollback)
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback} [key=value ...]\n' "$0" >&2
    exit 2
    ;;
esac
```

Với feature có thay đổi hệ thống, `check` chỉ trả `10` khi trạng thái chưa đạt,
`apply` thực hiện thay đổi và `verify` kiểm tra lại. Không dùng `eval`; luôn quote biến
đầu vào. Nên in kết quả bằng các marker `[PASS]`, `[FAIL]`, `[WARN]`, `[SKIP]` và
`[INFO]` để log trên TUI được highlight thống nhất.

### 3. Thêm profile nếu chạy từ Features hoặc CLI

Tạo hoặc sửa `profiles/<system>/<profile>.json`:

```json
{
  "apiVersion": "syssetup/v1",
  "name": "my-check-profile",
  "description": "Run custom system check",
  "features": [
    {
      "id": "my-check",
      "parameters": {
        "namespace": "pramf01",
        "show_pass": true
      }
    }
  ]
}
```

Profile không bắt buộc nếu feature chỉ được gọi từ workflow. Feature có đầy đủ default
cũng có thể được chọn thủ công trên TUI mà không cần profile riêng.

### 4. Thêm feature vào workflow

Workflow cần hai lớp file độc lập:

- `workflows/<workflow-id>/workflow.json`: định nghĩa thứ tự, dependency và chính sách lỗi.
- `workflow-configs/<config>.json`: cung cấp invocation, argument và artifact thực tế.

Thêm step vào workflow manifest:

```json
{
  "id": "my-check-step",
  "feature": "my-check"
}
```

Workflow local không cần server hoặc credential SSH:

```json
{
  "apiVersion": "syssetup/workflow/v1",
  "id": "my-validation",
  "name": "My validation",
  "version": "1.0.0",
  "executionMode": "local",
  "failurePolicy": "continue",
  "steps": [
    {
      "id": "my-check-step",
      "feature": "my-check"
    }
  ]
}
```

`failurePolicy: "continue"` vẫn chạy các invocation và step sau khi một bước thất bại;
`stop-server` dừng chuỗi step trên server bị lỗi. Trong config, workflow truyền nguyên
vẹn `args` vào script và không tự chạy chu trình `check -> apply -> verify`, vì vậy cần
ghi action mong muốn, thường là `verify`:

```json
{
  "apiVersion": "syssetup/workflow-config/v1",
  "workflow": "my-validation",
  "steps": {
    "my-check-step": [
      {
        "args": [
          "verify",
          "namespace=pramf01",
          "show_pass=true"
        ]
      }
    ]
  }
}
```

### 5. Truyền Excel, JSON hoặc script phụ bằng artifact

Không hard-code đường dẫn dữ liệu vào feature. Khai báo file trong invocation:

```json
{
  "args": ["verify", "namespace=pramf01"],
  "artifacts": [
    {
      "id": "input-file",
      "source": "artifacts/input.xlsx"
    },
    {
      "id": "checker",
      "source": "../features/my-check/check.py"
    }
  ]
}
```

Entrypoint đọc đường dẫn đã được tool cung cấp:

```bash
input_file="${SYSSETUP_ARTIFACT_INPUT_FILE:?missing input file}"
checker="${SYSSETUP_ARTIFACT_CHECKER:?missing checker}"
python3 "$checker" --input "$input_file"
```

Với local workflow, biến trỏ tới file local tuyệt đối. Với remote workflow, tool chuyển
nội dung qua SSH vào thư mục tạm có quyền hạn chế và tự xóa sau khi chạy.

### 6. Checklist trước khi phát hành

```bash
bash -n features/my-check/run.sh
go test ./...
go build -o bin/syssetup ./cmd/syssetup
./bin/syssetup list
./bin/syssetup workflows
./bin/syssetup plan --profile profiles/<system>/<profile>.json
```

Kiểm tra thêm cả nhánh thành công và thất bại, timeout, parameter sai, dependency thiếu,
quyền root và khả năng chạy lại. Khi thay đổi hành vi feature/workflow, tăng `version`
trong manifest tương ứng. `make package` tự đưa toàn bộ `features/`, `profiles/`,
`workflows/`, `workflow-configs/` và `checks/` vào gói phát hành.

Tóm tắt các file cần tạo hoặc sửa:

| Mục đích | File |
|---|---|
| Khai báo feature | `features/<id>/feature.json` |
| Adapter thực thi | `features/<id>/run.sh` |
| Logic/asset riêng | `features/<id>/*.py`, JSON, template... |
| Chọn feature và cấu hình parameter | `profiles/<system>/<profile>.json` |
| Định nghĩa thứ tự workflow | `workflows/<id>/workflow.json` |
| Truyền argument và artifact | `workflow-configs/<config>.json` |
| Kiểm thử và tài liệu chuyên biệt | `tests/*`, `checks/*`, `README.md` |

Chỉ cần sửa `internal/registry`, `internal/runner`, `internal/workflow`, `internal/ui` hoặc
`cmd/syssetup` khi feature cần một khả năng mới mà Feature API/Workflow API hiện tại chưa hỗ trợ.

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
