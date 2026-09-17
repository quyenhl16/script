#!/usr/bin/env bash
set -Eeuo pipefail

action="${1:-}"

if [[ -z "${NO_COLOR:-}" ]]; then
  bold=$'\033[1m'
  cyan=$'\033[1;36m'
  yellow=$'\033[1;33m'
  green=$'\033[0;32m'
  muted=$'\033[0;90m'
  reset=$'\033[0m'
else
  bold=''
  cyan=''
  yellow=''
  green=''
  muted=''
  reset=''
fi

print_title() {
  printf '%s%s\n' "$cyan" '======================================================================'
  printf '                     AMF SYSTEM VERIFICATION GUIDE\n'
  printf '                  HƯỚNG DẪN KIỂM TRA HỆ THỐNG AMF\n'
  printf '%s%s\n\n' '======================================================================' "$reset"
}

print_section() {
  local number="$1"
  local english_title="$2"
  local vietnamese_title="$3"
  printf '%s%s%s\n' "$yellow" '----------------------------------------------------------------------' "$reset"
  printf '%s%s. %s / %s%s\n' "$yellow" "$number" "$english_title" "$vietnamese_title" "$reset"
  printf '%s%s%s\n\n' "$yellow" '----------------------------------------------------------------------' "$reset"
}

print_item() {
  local index="$1"
  local english_title="$2"
  local vietnamese_title="$3"
  shift 3
  printf '%s%s%s %s%s%s\n' "$bold" "$index" "$reset" "$bold" "$english_title" "$reset"
  printf '    %s%s\n' "$bold" "$vietnamese_title$reset"
  while (($# >= 2)); do
    printf '    %s[ ]%s EN: %s\n' "$green" "$reset" "$1"
    printf '        VI: %s\n' "$2"
    shift 2
  done
  printf '\n'
}

print_note() {
  printf '%sNote / Lưu ý:%s %s\n' "$muted" "$reset" "$1"
  printf '               %s\n' "$2"
}

show_guide() {
  print_title

  print_section 1 'BEFORE INSTALLING THE AMF CNF' 'TRƯỚC KHI CÀI ĐẶT AMF CNF'
  printf '%sComplete the following prerequisites to install the AMF CNF successfully through MANO.%s\n\n' "$bold" "$reset"
  printf '%sHoàn thành các điều kiện tiên quyết sau để cài đặt AMF CNF thành công qua MANO.%s\n\n' "$bold" "$reset"
  print_item '1.1' 'Enable SCTP on RHEL 8 or later servers' 'Bật SCTP trên máy chủ RHEL 8 trở lên' \
    'If an installation server runs RHEL 8 or later, verify that SCTP is enabled.' \
    'Nếu máy chủ cài đặt chạy RHEL 8 trở lên, hãy xác nhận SCTP đã được bật.' \
    'Use the enable-sctp feature to install, enable, load, and verify the SCTP kernel module.' \
    'Dùng feature enable-sctp để cài đặt, bật, nạp và xác minh mô-đun SCTP của kernel.'
  print_item '1.2' 'Verify bond VLAN interfaces for OSPF' 'Xác minh các giao diện bond VLAN cho OSPF' \
    'If the system uses OSPF, verify the required bond VLAN interfaces on every server.' \
    'Nếu hệ thống sử dụng OSPF, hãy xác minh các giao diện bond VLAN bắt buộc trên từng máy chủ.' \
    'Use the verify-network feature to confirm that each bond VLAN exists and is UP.' \
    'Dùng feature verify-network để xác nhận từng bond VLAN tồn tại và đang ở trạng thái UP.' \
    'If a required bond VLAN does not exist, create it with the create-bond-vlan feature, then verify it again.' \
    'Nếu thiếu bond VLAN bắt buộc, hãy tạo bằng feature create-bond-vlan rồi xác minh lại.'
  print_item '1.3' 'Create local paths for services that use persistent volumes' 'Tạo đường dẫn cục bộ cho các dịch vụ sử dụng persistent volume' \
    'Use the create-local-path feature to prepare local paths on the target servers.' \
    'Dùng feature create-local-path để chuẩn bị các đường dẫn cục bộ trên máy chủ đích.' \
    'Create and verify paths for these PV-backed services: aerospike, netconfig, redis, ebm, nm, and nm-postgres.' \
    'Tạo và xác minh đường dẫn cho các dịch vụ dùng PV: aerospike, netconfig, redis, ebm, nm và nm-postgres.' \
    'Confirm the required owner, group, and permissions before starting the MANO installation.' \
    'Xác nhận owner, group và quyền truy cập bắt buộc trước khi bắt đầu cài đặt bằng MANO.'

  print_section 2 'AFTER INSTALLING THE AMF CNF' 'SAU KHI CÀI ĐẶT AMF CNF'
  printf '%sAfter the AMF system is up, perform the following checks.%s\n\n' "$bold" "$reset"
  printf '%sSau khi hệ thống AMF hoạt động, hãy thực hiện các kiểm tra sau.%s\n\n' "$bold" "$reset"
  print_item '2.1' 'Check environment variables' 'Kiểm tra biến môi trường' \
    'Use the k8s-env-check feature to compare workload environment variables with the approved baseline.' \
    'Dùng feature k8s-env-check để so sánh biến môi trường của workload với baseline đã được phê duyệt.' \
    'Review all missing, unexpected, and mismatched environment values.' \
    'Rà soát tất cả giá trị môi trường bị thiếu, ngoài dự kiến hoặc không khớp.'
  print_item '2.2' 'Check workload resources' 'Kiểm tra tài nguyên của workload' \
    'Use the k8s-resource-check feature to compare CPU and memory requests and limits with the approved baseline.' \
    'Dùng feature k8s-resource-check để so sánh request và limit CPU/bộ nhớ với baseline đã được phê duyệt.' \
    'Review all missing workloads, unexpected workloads, and resource mismatches.' \
    'Rà soát tất cả workload bị thiếu, workload ngoài dự kiến và tài nguyên không khớp.'
  print_item '2.3' 'Check Kubernetes services' 'Kiểm tra các dịch vụ Kubernetes' \
    'Use the k8s-service-check feature to verify required services, endpoints, external IPs, and TCP reachability.' \
    'Dùng feature k8s-service-check để xác minh service bắt buộc, endpoint, external IP và khả năng kết nối TCP.' \
    'Resolve every missing or unhealthy required service before continuing.' \
    'Xử lý mọi service bắt buộc bị thiếu hoặc không hoạt động tốt trước khi tiếp tục.'
  print_item '2.4' 'Check system connectivity' 'Kiểm tra kết nối hệ thống' \
    'Use the k8s-connectivity-check feature to verify VIPs, OSPF, ping paths, and peer NF HTTP connectivity.' \
    'Dùng feature k8s-connectivity-check để xác minh VIP, OSPF, đường ping và kết nối HTTP tới các NF ngang hàng.' \
    'Review every configured NF target and investigate all failed paths.' \
    'Rà soát từng NF đích đã cấu hình và điều tra tất cả đường kết nối bị lỗi.'
  print_item '2.5' 'Compare the running NetConf configuration with the reference XML' 'So sánh cấu hình NetConf đang chạy với XML tham chiếu' \
    'Use export-netconf-config to retrieve the AMF configuration currently running on the system.' \
    'Dùng export-netconf-config để lấy cấu hình AMF hiện đang chạy trên hệ thống.' \
    'Use compare-xml-config to compare the exported XML with the approved reference XML file.' \
    'Dùng compare-xml-config để so sánh XML đã xuất với tệp XML tham chiếu được phê duyệt.' \
    'Pay special attention to values under the configured criticalPaths and resolve every critical difference or missing required path.' \
    'Đặc biệt chú ý các giá trị trong criticalPaths đã cấu hình; xử lý mọi khác biệt nghiêm trọng hoặc đường dẫn bắt buộc bị thiếu.'

  print_section 3 'ADDITIONAL INFORMATION' 'THÔNG TIN BỔ SUNG'
  print_item '3.1' 'Recommended feature sequence' 'Trình tự feature được khuyến nghị' \
    'Before installation: check-os, enable-sctp, verify-network, verify-xml-config.' \
    'Trước khi cài đặt: check-os, enable-sctp, verify-network, verify-xml-config.' \
    'After installation: k8s-env-check, k8s-resource-check, k8s-service-check, k8s-connectivity-check.' \
    'Sau khi cài đặt: k8s-env-check, k8s-resource-check, k8s-service-check, k8s-connectivity-check.' \
    'Finish with export-netconf-config and compare-xml-config.' \
    'Kết thúc bằng export-netconf-config và compare-xml-config.'
  print_item '3.2' 'Troubleshooting and safety' 'Xử lý sự cố và an toàn' \
    'Review the first failed check before retrying dependent checks.' \
    'Rà soát kiểm tra bị lỗi đầu tiên trước khi chạy lại các kiểm tra phụ thuộc.' \
    'Do not change a production configuration without an approved rollback plan.' \
    'Không thay đổi cấu hình production khi chưa có kế hoạch rollback được phê duyệt.' \
    'Redact credentials, tokens, and sensitive subscriber data from shared evidence.' \
    'Ẩn thông tin xác thực, token và dữ liệu thuê bao nhạy cảm khỏi bằng chứng được chia sẻ.'
  print_item '3.3' 'Completion criteria' 'Tiêu chí hoàn thành' \
    'All mandatory checks pass or have an approved exception.' \
    'Tất cả kiểm tra bắt buộc đều pass hoặc có ngoại lệ được phê duyệt.' \
    'The exported running configuration and generated report are retained.' \
    'Cấu hình đang chạy đã xuất và báo cáo đã tạo được lưu giữ.' \
    'The installation result, exceptions, and follow-up actions are documented.' \
    'Kết quả cài đặt, các ngoại lệ và hành động tiếp theo được ghi lại.'

  print_note 'This feature displays guidance only and does not modify the system.' \
    'Feature này chỉ hiển thị hướng dẫn và không thay đổi hệ thống.'
}

case "$action" in
  check)
    # Always continue so the guide is displayed during verify.
    exit 10
    ;;
  apply)
    # This feature is informational and has no apply operation.
    :
    ;;
  verify)
    show_guide
    ;;
  rollback)
    # No system state is changed.
    :
    ;;
  *)
    printf 'Usage: %s {check|apply|verify|rollback}\n' "$0" >&2
    exit 2
    ;;
esac
