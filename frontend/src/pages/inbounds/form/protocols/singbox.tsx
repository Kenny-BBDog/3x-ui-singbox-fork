import { useTranslation } from 'react-i18next';
import { Form, Input } from 'antd';
import { useWatch } from 'react-hook-form';

import { FormField } from '@/components/form/rhf';

// Structural settings for sing-box anytls / hysteria2sb inbounds. The TLS
// block mirrors what the sing-box core reads: server_name (SNI), cert paths,
// ALPN. Client credentials live in the shared clients section (password
// field), exactly like other protocols.
export default function SingboxFields({ kind }: { kind: 'anytls' | 'hysteria2sb' }) {
  const { t } = useTranslation();

  const isHy2 = kind === 'hysteria2sb';

  return (
    <>
      <FormField
        name="settings.server_name"
        label={t('pages.inbounds.security.tls.serverName', 'TLS server name (SNI)')}
        rules={[{ required: true }]}
      >
        <Input placeholder="e.g. vpn.example.com" />
      </FormField>
      <FormField
        name="settings.certificate_path"
        label={t('pages.inbounds.security.tls.certPath', 'Certificate path (fullchain)')}
        rules={[{ required: true }]}
      >
        <Input placeholder="/etc/letsencrypt/live/example.com/fullchain.pem" />
      </FormField>
      <FormField
        name="settings.key_path"
        label={t('pages.inbounds.security.tls.keyPath', 'Key path (private key)')}
        rules={[{ required: true }]}
      >
        <Input placeholder="/etc/letsencrypt/live/example.com/privkey.pem" />
      </FormField>
      <FormField
        name="settings.alpn"
        label={t('pages.inbounds.security.tls.alpn', 'ALPN (comma separated)')}
      >
        <Input placeholder={isHy2 ? 'h3' : 'h2, http/1.1'} />
      </FormField>
      {isHy2 && (
        <>
          <FormField name="settings.obfs_type" label="Obfs type (optional)">
            <Input placeholder="salamander" />
          </FormField>
          <FormField name="settings.obfs_password" label="Obfs password (optional)">
            <Input />
          </FormField>
        </>
      )}
      <Form.Item
        label={t('pages.singbox.note', 'Server note')}
        extra={t(
          'pages.singbox.servedByNote',
          'Served by the sing-box core; Xray does not run this inbound.',
        )}
      >
        <span style={{ opacity: 0.65 }}>sing-box</span>
      </Form.Item>
    </>
  );
}

// re-export useWatch consumer so tree-shaking keeps the import minimal
export function SingboxAlpnPreview() {
  const alpn = useWatch({ name: 'settings.alpn' }) as string | undefined;
  return <span style={{ opacity: 0.5 }}>{alpn}</span>;
}