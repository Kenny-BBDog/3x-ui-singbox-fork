import { useTranslation } from 'react-i18next';
import { useQuery, useQueryClient } from '@tanstack/react-query';
import {
  Alert,
  Button,
  Card,
  Col,
  ConfigProvider,
  Descriptions,
  Layout,
  Modal,
  Popconfirm,
  Row,
  Statistic,
  message,
} from 'antd';
import { ReloadOutlined, ThunderboltOutlined } from '@ant-design/icons';

import { useTheme } from '@/hooks/useTheme';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import AppSidebar from '@/layouts/AppSidebar';
import { HttpUtil } from '@/utils';

interface SingboxStatus {
  running: boolean;
  version: string;
  lastError: string;
  binary: string;
}

// sing-box control page: status, restart, live config preview.
// Inbound/client CRUD for anytls / hysteria2sb uses the regular inbounds
// and clients pages — they are first-class rows in the shared tables.
export default function SingboxPage() {
  const { t } = useTranslation();
  const { antdThemeConfig } = useTheme();
  const { isMobile } = useMediaQuery();
  const queryClient = useQueryClient();
  const [messageApi, messageContextHolder] = message.useMessage();

  const pageClass = isMobile ? 'page-panel mobile' : 'page-panel';

  const status = useQuery<SingboxStatus>({
    queryKey: ['singbox-status'],
    queryFn: async () => {
      const res = await HttpUtil.get<SingboxStatus>('/panel/api/singbox/status', undefined, {
        silent: true,
      });
      return (res.obj as SingboxStatus) || { running: false, version: '', lastError: '', binary: '' };
    },
    refetchInterval: 5000,
  });

  const preview = useQuery<string>({
    queryKey: ['singbox-preview'],
    queryFn: async () => {
      const res = await HttpUtil.get<{ config: string }>('/panel/api/singbox/previewConfig', undefined, {
        silent: true,
      });
      return (res.obj as { config: string })?.config || '{}';
    },
  });

  const st = status.data;

  async function restart() {
    const res = await HttpUtil.post('/panel/api/singbox/restart', undefined, { silent: true });
    if (res.success) {
      messageApi.success('restarted');
      await queryClient.invalidateQueries({ queryKey: ['singbox-status'] });
    } else {
      messageApi.error(res.msg || 'restart failed');
    }
  }

  return (
    <ConfigProvider theme={antdThemeConfig}>
      {messageContextHolder}
      <Layout className={pageClass}>
        <AppSidebar />
        <Layout className="content-shell">
          <Layout.Content id="content-layout" className="content-area">
            <Row gutter={[16, 16]}>
              <Col xs={24} md={8}>
                <Card>
                  <Statistic
                    title="sing-box core"
                    value={st?.running ? 'running' : 'stopped'}
                    valueStyle={{ color: st?.running ? '#3f8600' : '#cf1322' }}
                    prefix={<ThunderboltOutlined />}
                  />
                  <Descriptions column={1} size="small" style={{ marginTop: 12 }}>
                    <Descriptions.Item label="Version">
                      {st?.version || '—'}
                    </Descriptions.Item>
                    <Descriptions.Item label="Binary">
                      <span style={{ fontSize: 12 }}>{st?.binary}</span>
                    </Descriptions.Item>
                  </Descriptions>
                  {st?.lastError ? (
                    <Alert type="error" showIcon message={st.lastError} style={{ marginTop: 8 }} />
                  ) : null}
                </Card>
              </Col>
              <Col xs={24} md={8}>
                <Card>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
                    <Popconfirm title="restart sing-box?" onConfirm={restart}>
                      <Button icon={<ReloadOutlined />} block>
                        Restart core
                      </Button>
                    </Popconfirm>
                    <Button
                      block
                      onClick={() => {
                        status.refetch();
                        preview.refetch();
                      }}
                    >
                      Refresh
                    </Button>
                  </div>
                </Card>
              </Col>
              <Col xs={24} md={8}>
                <Card title="How it works">
                  <div style={{ fontSize: 13, opacity: 0.75, lineHeight: 1.6 }}>
                    AnyTLS / Hysteria2 inbounds are created on the Inbounds page like any
                    other protocol. Clients, quotas, expiry, traffic and subscriptions work
                    identically because sing-box inbounds share the same tables as Xray.
                    This page controls the sing-box process itself.
                  </div>
                </Card>
              </Col>

              <Col span={24}>
                <Card title="Live sing-box config (rendered from DB)">
                  <pre
                    style={{
                      maxHeight: 480,
                      overflow: 'auto',
                      fontSize: 12,
                      background: 'rgba(128,128,128,0.06)',
                      padding: 12,
                      borderRadius: 8,
                    }}
                  >
                    {preview.data || '{}'}
                  </pre>
                </Card>
              </Col>
            </Row>
          </Layout.Content>
        </Layout>
      </Layout>
    </ConfigProvider>
  );
}