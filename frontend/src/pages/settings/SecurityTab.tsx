import { useCallback, useEffect, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Form, Input, Modal, Space, Spin, Switch, Tabs, message } from 'antd';
import { ApiOutlined, SafetyOutlined, TeamOutlined, UserOutlined } from '@ant-design/icons';
import { ClipboardManager, HttpUtil, IntlUtil, RandomUtil } from '@/utils';
import { SettingListItem } from '@/components/ui';
import { useMediaQuery } from '@/hooks/useMediaQuery';
import { catTabLabel } from './catTabLabel';
import TwoFactorModal from './TwoFactorModal';
import './SecurityTab.css';

interface ApiMsg<T = unknown> {
  success?: boolean;
  msg?: string;
  obj?: T;
}

interface ApiTokenRow {
  id: number;
  name: string;
  enabled: boolean;
  createdAt: number;
  scope: 'admin' | 'monitor' | 'node-sync';
  expiresAt: number;
}

interface PanelUser {
  id: number;
  username: string;
  twoFactorEnable: boolean;
  isSelf: boolean;
}

const UNIX_MILLISECONDS_THRESHOLD = 100_000_000_000;

function apiTokenCreatedAtMilliseconds(createdAt: number): number {
  return createdAt < UNIX_MILLISECONDS_THRESHOLD ? createdAt * 1000 : createdAt;
}

type TfaType = 'set' | 'confirm';

interface TfaState {
  open: boolean;
  title: string;
  description: string;
  token: string;
  type: TfaType;
  onConfirm: (success: boolean, code?: string) => void;
}

const TFA_INITIAL: TfaState = {
  open: false,
  title: '',
  description: '',
  token: '',
  type: 'set',
  onConfirm: () => {},
};

export default function SecurityTab() {
  const { t } = useTranslation();
  const { isMobile } = useMediaQuery();
  const [modal, modalContextHolder] = Modal.useModal();
  const [messageApi, messageContextHolder] = message.useMessage();

  const [tfa, setTfa] = useState<TfaState>(TFA_INITIAL);
  const [user, setUser] = useState({
    oldUsername: '',
    oldPassword: '',
    newUsername: '',
    newPassword: '',
  });
  const [updating, setUpdating] = useState(false);

  const [apiTokens, setApiTokens] = useState<ApiTokenRow[]>([]);
  const [apiTokensLoading, setApiTokensLoading] = useState(true);
  const [createOpen, setCreateOpen] = useState(false);
  const [createName, setCreateName] = useState('');
  const [creating, setCreating] = useState(false);
  const [createdToken, setCreatedToken] = useState<{ name: string; token: string } | null>(null);

  const [users, setUsers] = useState<PanelUser[]>([]);
  const [usersLoading, setUsersLoading] = useState(true);
  const [addOpen, setAddOpen] = useState(false);
  const [addUsername, setAddUsername] = useState('');
  const [addPassword, setAddPassword] = useState('');
  const [adding, setAdding] = useState(false);

  // The second factor belongs to the account, so the switch reads the signed-in
  // row rather than a panel-wide setting — and so does the credentials form.
  const selfTwoFactor = users.find((row) => row.isSelf)?.twoFactorEnable ?? false;

  const openTfa = useCallback((opts: Omit<TfaState, 'open'>) => {
    setTfa({ ...opts, open: true });
  }, []);

  const onTfaConfirm = useCallback(
    (success: boolean, code?: string) => {
      tfa.onConfirm(success, code);
    },
    [tfa],
  );

  function updateUserField<K extends keyof typeof user>(key: K, value: string) {
    setUser((prev) => ({ ...prev, [key]: value }));
  }

  const sendUpdateUser = useCallback(
    async (twoFactorCode = '') => {
      setUpdating(true);
      try {
        const msg = (await HttpUtil.post('/panel/api/setting/updateUser', {
          ...user,
          twoFactorCode,
        })) as ApiMsg;
        if (msg?.success) {
          await HttpUtil.post('/logout');
          const basePath = window.X_UI_BASE_PATH || '/';
          window.location.replace(basePath);
        }
      } finally {
        setUpdating(false);
      }
    },
    [user],
  );

  function onUpdateUserClick() {
    if (selfTwoFactor) {
      openTfa({
        title: t('pages.settings.security.twoFactorModalChangeCredentialsTitle'),
        description: t('pages.settings.security.twoFactorModalChangeCredentialsStep'),
        token: '',
        type: 'confirm',
        onConfirm: (ok: boolean, code?: string) => {
          if (ok) sendUpdateUser(code || '');
        },
      });
    } else {
      sendUpdateUser();
    }
  }

  const fetchApiTokens = useCallback(async () => {
    try {
      const msg = (await HttpUtil.get('/panel/api/setting/apiTokens')) as ApiMsg<ApiTokenRow[]>;
      if (msg?.success) setApiTokens(Array.isArray(msg.obj) ? msg.obj : []);
    } finally {
      setApiTokensLoading(false);
    }
  }, []);

  const loadApiTokens = useCallback(async () => {
    setApiTokensLoading(true);
    await fetchApiTokens();
  }, [fetchApiTokens]);

  const fetchUsers = useCallback(async () => {
    try {
      const msg = (await HttpUtil.get('/panel/api/users/list')) as ApiMsg<PanelUser[]>;
      if (msg?.success) setUsers(Array.isArray(msg.obj) ? msg.obj : []);
    } finally {
      setUsersLoading(false);
    }
  }, []);

  const loadUsers = useCallback(async () => {
    setUsersLoading(true);
    await fetchUsers();
  }, [fetchUsers]);

  useEffect(() => {
    void fetchApiTokens();
    void fetchUsers();
  }, [fetchApiTokens, fetchUsers]);

  async function copyToken(token: string) {
    if (!token) return;
    const ok = await ClipboardManager.copyText(token);
    if (ok) messageApi.success(t('copySuccess'));
    else messageApi.error(t('copyFail') ?? 'Copy failed');
  }

  function openCreateModal() {
    setCreateName('');
    setCreateOpen(true);
  }

  async function confirmCreateToken() {
    const name = createName.trim();
    if (!name) {
      messageApi.error(t('pages.settings.security.apiTokenNameRequired') || 'Name is required');
      return;
    }
    setCreating(true);
    try {
      const msg = (await HttpUtil.post('/panel/api/setting/apiTokens/create', { name })) as ApiMsg<{
        token?: string;
      }>;
      if (msg?.success) {
        setCreateOpen(false);
        await loadApiTokens();
        if (msg.obj?.token) {
          setCreatedToken({ name, token: msg.obj.token });
        }
      }
    } finally {
      setCreating(false);
    }
  }

  function confirmDeleteToken(row: ApiTokenRow) {
    modal.confirm({
      title: `${t('delete')} "${row.name}"?`,
      content:
        t('pages.settings.security.apiTokenDeleteWarning') ||
        'Any caller using this token will stop authenticating immediately.',
      okText: t('delete'),
      cancelText: t('cancel'),
      okType: 'danger',
      onOk: async () => {
        const msg = (await HttpUtil.post(`/panel/api/setting/apiTokens/delete/${row.id}`, {
          expectedScope: row.scope,
        })) as ApiMsg;
        if (msg?.success) await loadApiTokens();
      },
    });
  }

  async function toggleTokenEnabled(row: ApiTokenRow) {
    const target = !row.enabled;
    const msg = (await HttpUtil.post(`/panel/api/setting/apiTokens/setEnabled/${row.id}`, {
      enabled: target,
      expectedScope: row.scope,
    })) as ApiMsg;
    if (msg?.success) {
      setApiTokens((prev) => prev.map((r) => (r.id === row.id ? { ...r, enabled: target } : r)));
    }
  }

  function formatTokenDate(ts: number): string {
    if (!ts) return '';
    return IntlUtil.formatDate(apiTokenCreatedAtMilliseconds(ts));
  }

  async function postTwoFactor(enable: boolean, token: string, code: string) {
    const msg = (await HttpUtil.post('/panel/api/users/twoFactor', {
      enable,
      token,
      code,
    })) as ApiMsg;
    if (msg?.success) {
      messageApi.success(t('pages.settings.users.toasts.twoFactorSaved'));
      await loadUsers();
    } else if (msg?.msg) {
      messageApi.error(msg.msg);
    }
  }

  function toggleTwoFactor() {
    if (!selfTwoFactor) {
      const newToken = RandomUtil.randomBase32String();
      openTfa({
        title: t('pages.settings.security.twoFactorModalSetTitle'),
        description: '',
        token: newToken,
        type: 'set',
        onConfirm: (ok: boolean) => {
          if (ok) void postTwoFactor(true, newToken, '');
        },
      });
    } else {
      openTfa({
        title: t('pages.settings.security.twoFactorModalDeleteTitle'),
        description: t('pages.settings.security.twoFactorModalRemoveStep'),
        token: '',
        type: 'confirm',
        onConfirm: (ok: boolean, code?: string) => {
          if (ok) void postTwoFactor(false, '', code || '');
        },
      });
    }
  }

  function openAddUser() {
    setAddUsername('');
    setAddPassword('');
    setAddOpen(true);
  }

  async function confirmAddUser() {
    const username = addUsername.trim();
    if (!username || !addPassword) {
      messageApi.error(t('pages.settings.users.toasts.failed'));
      return;
    }
    setAdding(true);
    try {
      const msg = (await HttpUtil.post('/panel/api/users/add', {
        username,
        password: addPassword,
      })) as ApiMsg;
      if (msg?.success) {
        messageApi.success(t('pages.settings.users.toasts.added'));
        setAddOpen(false);
        await loadUsers();
      } else if (msg?.msg) {
        messageApi.error(msg.msg);
      }
    } finally {
      setAdding(false);
    }
  }

  function confirmRemoveUser(row: PanelUser) {
    modal.confirm({
      title: `${t('delete')} "${row.username}"?`,
      content: t('pages.settings.users.removeWarning'),
      okText: t('delete'),
      cancelText: t('cancel'),
      okType: 'danger',
      onOk: async () => {
        const msg = (await HttpUtil.post(`/panel/api/users/del/${row.id}`)) as ApiMsg;
        if (msg?.success) {
          messageApi.success(t('pages.settings.users.toasts.deleted'));
          await loadUsers();
        } else if (msg?.msg) {
          messageApi.error(msg.msg);
        }
      },
    });
  }

  return (
    <>
      {messageContextHolder}
      {modalContextHolder}
      <Tabs
        defaultActiveKey="1"
        items={[
          {
            key: '1',
            label: catTabLabel(<UserOutlined />, t('pages.settings.security.admin'), isMobile),
            children: (
              <>
                <SettingListItem paddings="small" title={t('pages.settings.oldUsername')}>
                  <Input
                    value={user.oldUsername}
                    autoComplete="username"
                    onChange={(e) => updateUserField('oldUsername', e.target.value)}
                  />
                </SettingListItem>
                <SettingListItem paddings="small" title={t('pages.settings.currentPassword')}>
                  <Input.Password
                    value={user.oldPassword}
                    autoComplete="current-password"
                    onChange={(e) => updateUserField('oldPassword', e.target.value)}
                  />
                </SettingListItem>
                <SettingListItem paddings="small" title={t('pages.settings.newUsername')}>
                  <Input
                    value={user.newUsername}
                    onChange={(e) => updateUserField('newUsername', e.target.value)}
                  />
                </SettingListItem>
                <SettingListItem paddings="small" title={t('pages.settings.newPassword')}>
                  <Input.Password
                    value={user.newPassword}
                    autoComplete="new-password"
                    onChange={(e) => updateUserField('newPassword', e.target.value)}
                  />
                </SettingListItem>
                <div className="security-actions">
                  <Space style={{ padding: '0 20px' }}>
                    <Button type="primary" loading={updating} onClick={onUpdateUserClick}>
                      {t('confirm')}
                    </Button>
                  </Space>
                </div>
              </>
            ),
          },
          {
            key: '2',
            label: catTabLabel(
              <SafetyOutlined />,
              t('pages.settings.security.twoFactor'),
              isMobile,
            ),
            children: (
              <SettingListItem
                paddings="small"
                title={t('pages.settings.security.twoFactorEnable')}
                description={t('pages.settings.security.twoFactorEnableDesc')}
              >
                <Switch checked={selfTwoFactor} onClick={toggleTwoFactor} />
              </SettingListItem>
            ),
          },
          {
            key: '3',
            label: catTabLabel(<TeamOutlined />, t('pages.settings.users.tab'), isMobile),
            children: (
              <div className="api-token-section">
                <div className="api-token-header">
                  <p className="api-token-hint">{t('pages.settings.users.hint')}</p>
                  <Button type="primary" size="small" onClick={openAddUser}>
                    + {t('pages.settings.users.add')}
                  </Button>
                </div>
                <Spin spinning={usersLoading}>
                  {users.map((row) => (
                    <div key={row.id} className="api-token-row">
                      <div className="api-token-row-head">
                        <div className="api-token-name-wrap">
                          <span className="api-token-name">{row.username}</span>
                          {row.isSelf ? (
                            <span className="api-token-created">
                              {t('pages.settings.users.you')}
                            </span>
                          ) : null}
                          {row.twoFactorEnable ? (
                            <span className="api-token-created">
                              {t('pages.settings.security.twoFactor')}
                            </span>
                          ) : null}
                        </div>
                        <div className="api-token-actions">
                          <Button
                            size="small"
                            danger
                            type="text"
                            disabled={row.isSelf}
                            onClick={() => confirmRemoveUser(row)}
                          >
                            {t('delete')}
                          </Button>
                        </div>
                      </div>
                    </div>
                  ))}
                </Spin>
              </div>
            ),
          },
          {
            key: '4',
            label: catTabLabel(<ApiOutlined />, t('pages.nodes.apiToken'), isMobile),
            children: (
              <div className="api-token-section">
                <div className="api-token-header">
                  <p className="api-token-hint">{t('pages.nodes.apiTokenHint')}</p>
                  <Button type="primary" size="small" onClick={openCreateModal}>
                    + {t('pages.settings.security.apiTokenNew') || 'New token'}
                  </Button>
                </div>
                <Spin spinning={apiTokensLoading}>
                  {apiTokens.map((row) => (
                    <div key={row.id} className={`api-token-row${row.enabled ? '' : ' disabled'}`}>
                      <div className="api-token-row-head">
                        <div className="api-token-name-wrap">
                          <span className="api-token-name">{row.name}</span>
                          <span className="api-token-created">
                            {formatTokenDate(row.createdAt)}
                          </span>
                        </div>
                        <div className="api-token-actions">
                          <Switch
                            size="small"
                            checked={row.enabled}
                            onChange={() => toggleTokenEnabled(row)}
                          />
                          <Button
                            size="small"
                            danger
                            type="text"
                            onClick={() => confirmDeleteToken(row)}
                          >
                            {t('delete')}
                          </Button>
                        </div>
                      </div>
                    </div>
                  ))}
                </Spin>
              </div>
            ),
          },
        ]}
      />

      <Modal
        open={addOpen}
        title={t('pages.settings.users.add')}
        confirmLoading={adding}
        okText={t('confirm')}
        cancelText={t('cancel')}
        onOk={confirmAddUser}
        onCancel={() => setAddOpen(false)}
      >
        <p className="api-token-hint">{t('pages.settings.users.addHint')}</p>
        <Form layout="vertical">
          <Form.Item label={t('username')} required>
            <Input
              value={addUsername}
              maxLength={64}
              autoComplete="off"
              onChange={(e) => setAddUsername(e.target.value)}
            />
          </Form.Item>
          <Form.Item label={t('password')} required>
            <Input.Password
              value={addPassword}
              autoComplete="new-password"
              onChange={(e) => setAddPassword(e.target.value)}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={createOpen}
        title={t('pages.settings.security.apiTokenNew') || 'New API token'}
        confirmLoading={creating}
        okText={t('confirm')}
        cancelText={t('cancel')}
        onOk={confirmCreateToken}
        onCancel={() => setCreateOpen(false)}
      >
        <Form layout="vertical">
          <Form.Item label={t('pages.settings.security.apiTokenName') || 'Name'} required>
            <Input
              value={createName}
              maxLength={64}
              placeholder={
                t('pages.settings.security.apiTokenNamePlaceholder') || 'e.g. central-panel-a'
              }
              onChange={(e) => setCreateName(e.target.value)}
              onPressEnter={confirmCreateToken}
            />
          </Form.Item>
        </Form>
      </Modal>

      <Modal
        open={!!createdToken}
        title={t('pages.settings.security.apiTokenCreatedTitle') || 'Token created'}
        okText={t('done')}
        onOk={() => setCreatedToken(null)}
        onCancel={() => setCreatedToken(null)}
        cancelButtonProps={{ style: { display: 'none' } }}
      >
        <p className="api-token-created-notice">
          {t('pages.settings.security.apiTokenCreatedNotice') ||
            'Copy this token now. For security it is not stored in readable form and will not be shown again.'}
        </p>
        <div className="api-token-value-wrap">
          <code className="api-token-value">{createdToken?.token}</code>
          <Button
            size="small"
            type="primary"
            onClick={() => createdToken && copyToken(createdToken.token)}
          >
            {t('copy')}
          </Button>
        </div>
      </Modal>

      <TwoFactorModal
        open={tfa.open}
        title={tfa.title}
        description={tfa.description}
        token={tfa.token}
        type={tfa.type}
        onConfirm={onTfaConfirm}
        onOpenChange={(open) => setTfa((prev) => ({ ...prev, open }))}
      />
    </>
  );
}
