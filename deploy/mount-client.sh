#!/usr/bin/env bash
# mount-client.sh — 给一个客户追加/更新到全部（或挑选的）anytls 入站
# 用法（DMIT 上, root）:
#   ./mount-client.sh add    <email> <subId> [totalGB] [tag1 tag2 ...]   # 挂到指定入站（缺省=全部12个）
#   ./mount-client.sh set    <email> <subId> tag1 tag2 ...               # 挂载改为恰好这些入站
#   ./mount-client.sh unlink <email> [tag ...]                            # 从某些/全部入站摘除
# tag 用入站 tag: inbound-dmit-anytls / inbound-la-anytls / res-30001..30010
# 例:
#   ./mount-client.sh add liu xks83ndq 214748364800 inbound-la-anytls res-30009
#   ./mount-client.sh set  liu xks83ndq inbound-la-anytls res-30009
set -euo pipefail

DB=/etc/x-ui/x-ui.db
CMD=${1:-}; EMAIL=${2:-}; SUBID=${3:-}

if [[ -z "$CMD" || -z "$EMAIL" ]]; then
  echo "usage: $0 add|set|unlink <email> <subId> [totalGB] [tags...]"; exit 1
fi
shift 3 2>/dev/null || shift 2

python3 - "$CMD" "$EMAIL" "$SUBID" "$@" << 'PYEOF'
import sys, json, sqlite3, secrets, subprocess

cmd, email, subid = sys.argv[1], sys.argv[2], sys.argv[3]
rest = sys.argv[4:]
total_gb = 214748364800  # default 200 GB
if cmd == 'add' and rest and rest[0].isdigit():
    total_gb = int(rest[0]); rest = rest[1:]

db = sqlite3.connect('/etc/x-ui/x-ui.db')
cur = db.cursor()

if cmd == 'add':
    # append the client record (subId row must already exist from panel UI, else create)
    if not cur.execute('SELECT 1 FROM clients WHERE email=?', (email,)).fetchone():
        cur.execute('INSERT INTO clients (email, sub_id, enable, total_gb, created_at) VALUES (?, ?, 1, ?, strftime("%s","now")*1000)',
                    (email, subid, total_gb))
        db.commit()
    cid = cur.execute('SELECT id FROM clients WHERE email=?', (email,)).fetchone()[0]

ALL_TAGS = ['inbound-dmit-anytls', 'inbound-la-anytls'] + ['res-300%02d' % i for i in range(1, 11)]
want = rest if rest else ALL_TAGS
if cmd == 'add':
    for tag in want:
        row = cur.execute('SELECT id, settings FROM inbounds WHERE tag=?', (tag,)).fetchone()
        if not row:
            print('tag not found:', tag); continue
        iid, s_raw = row
        s = json.loads(s_raw)
        cl = s.get('clients', [])
        if any(x.get('email') == email for x in cl):
            continue
        # try to reuse an existing password from any inbound for this email (cross-machine consistency)
        pwd = None
        for (s2,) in cur.execute('SELECT settings FROM inbounds WHERE protocol="anytls"').fetchall():
            for x in (json.loads(s2 or '{}').get('clients') or []):
                if x.get('email') == email and x.get('password'):
                    pwd = x['password']; break
            if pwd: break
        if not pwd:
            pwd = 'atls-' + secrets.token_hex(12)
        cl.append({'id': email + '-slot', 'email': email, 'password': pwd, 'limitIp': 0,
                   'totalGB': total_gb, 'expiryTime': 0, 'enable': True, 'flow': '',
                   'subId': subid, 'reset': 0, 'resetDay': 0, 'resetMax': 0, 'resetWeekday': 0,
                   'trafficReset': 'never', 'trafficResetDay': 1, 'tgId': 0, 'comment': ''})
        cur.execute('UPDATE inbounds SET settings=? WHERE id=?', (json.dumps(s, ensure_ascii=False, indent=1), iid))
        cur.execute('INSERT OR IGNORE INTO client_inbounds (client_id, inbound_id, flow_override) VALUES (?, ?, "")', (cid, iid))
elif cmd == 'set':
    all_tags = [t for t in ALL_TAGS]
    cid = cur.execute('SELECT id FROM clients WHERE email=?', (email,)).fetchone()
    if not cid:
        print('client not found:', email); sys.exit(1)
    cid = cid[0]
    for tag in all_tags:
        row = cur.execute('SELECT id, settings FROM inbounds WHERE tag=?', (tag,)).fetchone()
        if not row: continue
        iid, s_raw = row
        s = json.loads(s_raw)
        cl = s.get('clients', [])
        mounted = cur.execute('SELECT 1 FROM client_inbounds WHERE client_id=? AND inbound_id=?', (cid, iid)).fetchone()
        if tag in want:
            if not mounted:
                # ensure settings entry
                if not any(x.get('email') == email for x in cl):
                    pwd = None
                    for (s2,) in cur.execute('SELECT settings FROM inbounds WHERE protocol="anytls"').fetchall():
                        for x in (json.loads(s2 or '{}').get('clients') or []):
                            if x.get('email') == email and x.get('password'):
                                pwd = x['password']; break
                        if pwd: break
                    if not pwd:
                        pwd = 'atls-' + secrets.token_hex(12)
                    cl.append({'id': email + '-slot', 'email': email, 'password': pwd, 'limitIp': 0,
                               'totalGB': 0, 'expiryTime': 0, 'enable': True, 'flow': '',
                               'subId': subid, 'reset': 0, 'resetDay': 0, 'resetMax': 0, 'resetWeekday': 0,
                               'trafficReset': 'never', 'trafficResetDay': 1, 'tgId': 0, 'comment': ''})
                cur.execute('INSERT OR IGNORE INTO client_inbounds (client_id, inbound_id, flow_override) VALUES (?, ?, "")', (cid, iid))
        else:
            if mounted:
                s['clients'] = [x for x in cl if x.get('email') != email]
                cur.execute('DELETE FROM client_inbounds WHERE client_id=? AND inbound_id=?', (cid, iid))
        cur.execute('UPDATE inbounds SET settings=? WHERE id=?', (json.dumps(s, ensure_ascii=False, indent=1), iid))
elif cmd == 'unlink':
    cid = cur.execute('SELECT id FROM clients WHERE email=?', (email,)).fetchone()
    if not cid:
        print('client not found'); sys.exit(1)
    cid = cid[0]
    tags = rest if rest else ALL_TAGS
    for tag in tags:
        row = cur.execute('SELECT id, settings FROM inbounds WHERE tag=?', (tag,)).fetchone()
        if not row: continue
        iid, s_raw = row
        s = json.loads(s_raw)
        s['clients'] = [x for x in s.get('clients', []) if x.get('email') != email]
        cur.execute('UPDATE inbounds SET settings=? WHERE id=?', (json.dumps(s, ensure_ascii=False, indent=1), iid))
        cur.execute('DELETE FROM client_inbounds WHERE client_id=? AND inbound_id=?', (cid, iid))

db.commit()
mounts = sorted(m[0] for m in cur.execute('SELECT inbound_id FROM client_inbounds ci JOIN clients c ON c.id=ci.client_id WHERE c.email=?', (email,)).fetchall())
print('mounts for', email, '->', mounts)
PYEOF

echo "restarting panels..."
systemctl restart x-ui >/dev/null 2>&1
ssh -i /root/.ssh/sub2api_migration_ed25519 -o StrictHostKeyChecking=no root@156.225.88.212 \
    "systemctl restart x-ui >/dev/null 2>&1" || true
sleep 12
echo "verify: curl -sk -A 'Shadowrocket/2230' https://vpn.flintic.uk/p/$SUBID"