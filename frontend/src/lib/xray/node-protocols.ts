import { Protocols } from '@/schemas/primitives';

/*
 * Protocols whose inbounds can live on a sub-node (the "Deploy To" set),
 * shared by the inbound form and the clone dialog so the two cannot drift.
 */
export const NODE_ELIGIBLE_PROTOCOLS: Readonly<Record<string, true>> = {
  [Protocols.VLESS]: true,
  [Protocols.VMESS]: true,
  [Protocols.TROJAN]: true,
  [Protocols.SHADOWSOCKS]: true,
  [Protocols.HYSTERIA]: true,
  [Protocols.WIREGUARD]: true,
  [Protocols.ANYTLS]: true,
  [Protocols.HYSTERIA2SB]: true,
};
