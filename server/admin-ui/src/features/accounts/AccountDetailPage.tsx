import { useQuery } from '@tanstack/react-query'
import { ArrowRight, ChevronLeft, KeyRound, X } from 'lucide-react'
import { type CSSProperties, useEffect, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { Link, useLocation, useParams } from 'react-router-dom'
import { CopyValue } from '../../CopyValue'
import { adminApi } from '../../api'
import { accountColor } from '../../colors'
import { tr } from '../../i18n'
import { connectionLink, readAccountReturn, resourceOriginState, usePageState, useResourceTabState } from '../../pageState'
import { QueryStatus, useAutoRefresh } from '../../refresh'
import { AccountMark, ErrorBlock, PendingBlock, ResourceTabs, formatDate } from '../../shared/console'
import type { AdminPasswordResetCodeResponse } from '../../types'
import { useOverlay } from '../../useOverlay'
import { DevicesPage } from '../devices/DevicesPage'
import { NetworksPage } from '../networks/NetworksPage'
import { RelationshipsPage } from '../relationships/RelationshipsPage'

function PasswordResetCodeDialog({ accountId, username, onClose }: { accountId: string; username: string; onClose: () => void }) {
  const dialogRef = useRef<HTMLDialogElement>(null)
  const [result, setResult] = useState<AdminPasswordResetCodeResponse>()
  const [error, setError] = useState('')
  const [submitting, setSubmitting] = useState(false)
  useOverlay(true, submitting ? () => {} : onClose)

  useEffect(() => {
    const dialog = dialogRef.current
    if (!dialog) return
    dialog.showModal()
    return () => dialog.close()
  }, [])

  const issueCode = async () => {
    setSubmitting(true)
    setError('')
    try {
      setResult(await adminApi.issuePasswordResetCode(accountId))
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : tr('生成恢复码失败'))
    } finally {
      setSubmitting(false)
    }
  }

  return createPortal(<dialog
    ref={dialogRef}
    className="account-reset-dialog"
    aria-labelledby="account-reset-title"
    onCancel={(event) => { event.preventDefault(); if (!submitting) onClose() }}
    onClick={(event) => {
      if (event.target !== event.currentTarget || submitting) return
      const bounds = event.currentTarget.getBoundingClientRect()
      if (event.clientX < bounds.left || event.clientX > bounds.right || event.clientY < bounds.top || event.clientY > bounds.bottom) onClose()
    }}
  >
    <header className="account-reset-dialog-head">
      <div><span>{tr('账号')}: {username}</span><h2 id="account-reset-title">{tr('密码恢复码')}</h2></div>
      <button type="button" className="icon-button-v2" onClick={onClose} disabled={submitting} aria-label={tr('关闭恢复码窗口')}><X size={17} /></button>
    </header>
    <section className="account-reset-dialog-body">
      {result ? <>
        <p className="account-reset-success" role="status">{tr('恢复码已生成。')}</p>
        <p>{tr('恢复码仅显示这一次，有效期 15 分钟。请通过可信的站外渠道交给账号所有者；关闭窗口后无法再次查看。')}</p>
        <div className="account-reset-code-row"><code>{result.code}</code><CopyValue value={result.code} label="复制恢复码" /></div>
        <p className="account-reset-expiry">{tr('恢复码有效期至：')}{formatDate(result.expires_at)}</p>
        <footer><button type="button" className="button primary" onClick={onClose}>{tr('完成')}</button></footer>
      </> : <>
        <p>{tr('为此账号生成一次性恢复码？新码会立即使此前签发的恢复码失效。')}</p>
        {error && <div className="account-reset-error" role="alert"><strong>{tr('生成恢复码失败')}</strong><span>{error}</span></div>}
        <footer>
          <button type="button" className="button secondary" onClick={onClose} disabled={submitting}>{tr('取消')}</button>
          <button type="button" className="button primary" onClick={() => void issueCode()} disabled={submitting}><KeyRound size={15} />{tr(submitting ? '正在生成…' : '生成恢复码')}</button>
        </footer>
      </>}
    </section>
  </dialog>, document.body)
}

export function AccountDetailPage() {
  const refreshInterval = useAutoRefresh()
  const { id = '' } = useParams()
  const location = useLocation()
  const origin = readAccountReturn(location.state)
  const { params } = usePageState()
  const [showResetDialog, setShowResetDialog] = useState(false)
  const tab = ['devices', 'networks', 'rooms'].includes(params.get('tab') || '') ? params.get('tab')! : 'topology'
  const setTab = useResourceTabState(tab, 'topology', ['topology', 'devices', 'networks', 'rooms'])
  useEffect(() => { window.scrollTo(0, 0) }, [id, tab])
  const detail = useQuery({ queryKey: ['account-summary', id], queryFn: ({ signal }) => adminApi.accountSummary(id, signal), enabled: Boolean(id), refetchInterval: refreshInterval })
  const account = detail.data?.account
  return <div className="page-stack">
    <div className="detail-navigation"><Link className="text-link" to={origin.to} state={origin.state}><ChevronLeft size={15} />{tr(origin.label)}</Link><div className="account-detail-actions"><button type="button" className="button secondary compact" onClick={() => setShowResetDialog(true)}><KeyRound size={15} />{tr('生成密码恢复码')}</button><Link state={resourceOriginState(location)} className="button secondary compact" to={connectionLink({ accountId: id })}>{tr('查看账号连接')}<ArrowRight size={15} /></Link></div></div>
    <QueryStatus queries={[detail]} />
    {detail.isPending ? <PendingBlock queries={[detail]} label="正在加载账号…" /> : detail.error && !account ? <ErrorBlock error={detail.error} onRetry={() => { void detail.refetch() }} /> : account ? <>
      <section className="account-hero">
        <AccountMark account={account} size="large" />
        <div className="account-hero-copy"><span className="account-color-label" style={{ '--account-mark-color': accountColor(account.id) } as CSSProperties}>{tr('ACCOUNT')}</span><h2>{account.username}</h2><p>{account.email}</p><span className="value-with-action"><code>{account.id}</code><CopyValue value={account.id} label="复制账号 ID" /></span></div>
        <div className="account-hero-stats"><div><strong>{account.device_count}</strong><span>{tr('设备')}</span></div><div><strong className="positive-text">{account.online_devices}</strong><span>{tr('在线')}</span></div><div><strong>{account.network_count}</strong><span>{tr('网络')}</span></div><div><strong>{account.room_count}</strong><span>{tr('房间')}</span></div></div>
      </section>
      <ResourceTabs id="account-resources" label="账号资源" value={tab} onChange={setTab} tabs={[
        { value: 'topology', label: tr('关系') }, { value: 'devices', label: `${tr('设备')} ${account.device_count}` }, { value: 'networks', label: `${tr('全部网络')} ${account.network_count}` }, { value: 'rooms', label: `${tr('房间网络')} ${account.room_count}` },
      ]} />
      <div role="tabpanel" id="account-resources-panel" aria-labelledby={`account-resources-${tab}`} tabIndex={0}>
        {tab === 'topology' && <RelationshipsPage accountScope={account} />}
        {tab === 'devices' && <DevicesPage accountScope={account} />}
        {(tab === 'networks' || tab === 'rooms') && <NetworksPage accountScope={account} fixedTab={tab} />}
      </div>
    </> : <ErrorBlock error={new Error('控制面未返回该账号详情，请返回账号列表重试。')} onRetry={() => { void detail.refetch() }} />}
    {account && showResetDialog && <PasswordResetCodeDialog accountId={account.id} username={account.username} onClose={() => setShowResetDialog(false)} />}
  </div>
}
