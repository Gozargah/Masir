#!/bin/bash
# Disposable Ubuntu Lima guest only. Host supplies /tmp/old-masir (v0.1.0)
# and /tmp/components (v0.2.0, signed with an ephemeral fixture key embedded in it).
# Neither binary is suitable for production. The helper runs in an isolated network
# namespace; the system service remains under the guest's normal supervisor.
set -euo pipefail
rm -rf /tmp/components-tampered /tmp/components-bad-signature
uid=$(id -u)
config="${XDG_CONFIG_HOME:-$HOME/.config}/masir"
service_dir=/usr/local/lib/masir
sudo /tmp/old-masir daemon enable --user "$uid"
sudo sh -c 'gzip -dc /tmp/components/geoip.dat.gz > /usr/local/lib/masir/geoip.dat; gzip -dc /tmp/components/geosite.dat.gz > /usr/local/lib/masir/geosite.dat'
sudo rm -f "$service_dir/installed-geo.json"
# Append a valid unknown protobuf field: custom bytes remain valid geo data.
printf '\240\006\001' | sudo tee -a "$service_dir/geosite.dat" >/dev/null
before=$(sudo sha256sum "$service_dir/masir")
geo_before=$(sudo sha256sum "$service_dir/geoip.dat" "$service_dir/geosite.dat")
pid_before=$(systemctl show masird.service --property MainPID --value)
cp -R /tmp/components /tmp/components-bad-signature
printf ' ' >> /tmp/components-bad-signature/service-update.json
if sudo unshare --net /tmp/components/masir daemon import-desktop --user "$uid" --from /tmp/components-bad-signature; then
 echo 'FAIL: invalid signature accepted'; exit 1
fi
[ "$(systemctl show masird.service --property MainPID --value)" = "$pid_before" ]
[ "$(sudo sha256sum "$service_dir/masir")" = "$before" ]
echo 'PASS: invalid signature rejected before service stop'
cp -R /tmp/components /tmp/components-tampered
printf 'tamper' >> /tmp/components-tampered/masir
if sudo unshare --net /tmp/components/masir daemon import-desktop --user "$uid" --from /tmp/components-tampered; then
 echo 'FAIL: tampered binary accepted'; exit 1
fi
[ "$(systemctl show masird.service --property MainPID --value)" = "$pid_before" ]
[ "$(sudo sha256sum "$service_dir/masir")" = "$before" ]
echo 'PASS: tampered bytes rejected without stopping or replacing old service'
cp "$config/routing.json" /tmp/offline-routing.previous
restore_rules() { cp /tmp/offline-routing.previous "$config/routing.json"; }
trap restore_rules EXIT
python3 - "$config/routing.json" <<'PY'
import json,sys
p=sys.argv[1]
v=json.load(open(p));v['rules']=[{'tag':'offline-invalid','match':{'port':'not-a-port'},'target':{'kind':'direct'}}]
json.dump(v,open(p,'w'))
PY
if sudo unshare --net /tmp/components/masir daemon import-desktop --user "$uid" --from /tmp/components; then
 echo 'FAIL: incompatible saved rule accepted'; exit 1
fi
[ "$(systemctl show masird.service --property MainPID --value)" = "$pid_before" ]
restore_rules
trap - EXIT
echo 'PASS: preflight failure leaves the old service running'
sudo unshare --net /tmp/components/masir daemon import-desktop --user "$uid" --from /tmp/components
[ "$("$service_dir/masir" --version)" = 'masir version 0.2.0' ]
[ "$(sudo sha256sum "$service_dir/geoip.dat" "$service_dir/geosite.dat")" = "$geo_before" ]
[ "$(sudo sha256sum "$service_dir/masir.previous" | cut -d ' ' -f1)" = "$(echo "$before" | cut -d ' ' -f1)" ]
systemctl is-active --quiet masird.service
"$service_dir/masir" version | grep '^daemon 0.2.0$'
echo 'PASS: offline service upgrade, custom geo preservation, rollback binary and restart'
# A lost GUI response may retry an already installed payload. Test the managed
# geo case too: the custom-geo path has its own binary-identity optimization.
rollback_before=$(sudo sha256sum "$service_dir/masir.previous" "$service_dir/geoip.dat.previous" "$service_dir/geosite.dat.previous")
sudo sh -c 'gzip -dc /tmp/components/geoip.dat.gz > /usr/local/lib/masir/geoip.dat; gzip -dc /tmp/components/geosite.dat.gz > /usr/local/lib/masir/geosite.dat'
sudo python3 - <<'PY'
import json
manifest=json.load(open('/tmp/components/service-update.json'))
pair={name:manifest['files'][name] for name in ['geoip.dat','geosite.dat']}
json.dump({'version':1,'files':pair},open('/usr/local/lib/masir/installed-geo.json','w'))
PY
sudo unshare --net /tmp/components/masir daemon import-desktop --user "$uid" --from /tmp/components
[ "$(sudo sha256sum "$service_dir/masir.previous" "$service_dir/geoip.dat.previous" "$service_dir/geosite.dat.previous")" = "$rollback_before" ]
echo 'PASS: identical managed-payload retry preserves original rollback files'
