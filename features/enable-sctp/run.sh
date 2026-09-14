#!/usr/bin/env bash
set -Eeuo pipefail

fail() {
  printf 'Error: %s\n' "$*" >&2
  exit 1
}

(( EUID == 0 )) || fail "Vui lòng chạy script với quyền root."

[[ -r /etc/os-release ]] || fail "/etc/os-release không tồn tại hoặc không đọc được"
os_id="$(sed -n 's/^ID=//p' /etc/os-release | head -n1 | tr -d '\"')"
version_id="$(sed -n 's/^VERSION_ID=//p' /etc/os-release | head -n1 | tr -d '\"')"
major_version="${version_id%%.*}"
[[ "$os_id" == rhel ]] || fail "Chỉ hỗ trợ Red Hat Enterprise Linux; hệ điều hành hiện tại: ${os_id:-unknown}"
[[ "$major_version" =~ ^[0-9]+$ ]] || fail "Không xác định được phiên bản RHEL: ${version_id:-unknown}"
(( 10#$major_version >= 8 )) || fail "Yêu cầu RHEL 8 trở lên; phiên bản hiện tại: $version_id"

for command in dnf uname sed tail modprobe lsmod grep rpm; do
  command -v "$command" >/dev/null 2>&1 || fail "Không tìm thấy lệnh bắt buộc: $command"
done

kernel_package="kernel-modules-extra-$(uname -r)"

echo "==> 1. Cài đặt kernel-modules-extra..."
dnf install -y "$kernel_package"
rpm -q "$kernel_package" >/dev/null || fail "Gói chưa được cài đặt thành công: $kernel_package"

echo "==> 2. Tạo và ghi /etc/modules-load.d/sctp.conf..."
printf 'sctp\n' > /etc/modules-load.d/sctp.conf

echo "==> 3. Comment dòng cuối trong các file blacklist SCTP..."
for file in \
  /etc/modprobe.d/sctp-blacklist.conf \
  /etc/modprobe.d/sctp_diag-blacklist.conf
do
  if [[ ! -f "$file" ]]; then
    echo "Cảnh báo: Không tìm thấy $file"
    continue
  fi

  last_line="$(tail -n 1 -- "$file")"
  if [[ "$last_line" =~ ^[[:space:]]*$ ]]; then
    echo "Bỏ qua dòng cuối trống: $file"
  elif [[ "$last_line" =~ ^[[:space:]]*# ]]; then
    echo "Dòng cuối đã được comment: $file"
  else
    sed -i '$s/^[[:space:]]*/# /' "$file"
    echo "Đã comment dòng cuối: $file"
  fi
done

echo "==> 4. Load SCTP module..."
modprobe sctp

echo "==> 5. Kiểm tra SCTP module..."
grep -Eq '^[[:space:]]*sctp[[:space:]]*$' /etc/modules-load.d/sctp.conf || \
  fail "Cấu hình tự động load SCTP không hợp lệ"
lsmod | grep -E '^sctp[[:space:]]'

echo "==> Hoàn tất."
