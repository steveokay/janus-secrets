import { GitBranch, Boxes, KeyRound } from 'lucide-react'
import { ConnectorCard, StatusLine } from './ConnectorCard'
import { useIntegrationStatus } from './useIntegrationStatus'

export function IntegrationsPage() {
  const s = useIntegrationStatus()
  return (
    <div className="mx-auto max-w-5xl p-6">
      <header className="mb-5">
        <h1 className="text-[20px] font-semibold text-ink">Integrations</h1>
        <p className="mt-1 text-[13px] text-ink-mute">
          Connect Janus to external systems. Configure each below.
        </p>
      </header>
      <div className="grid gap-4 sm:grid-cols-2 lg:grid-cols-3">
        <ConnectorCard
          icon={<GitBranch size={18} />}
          title="GitHub"
          description="Sync secrets to GitHub Actions, and let CI pull secrets keyless via OIDC federation."
          statuses={
            <>
              <StatusLine label="Actions sync" value={s.githubSync} />
              <StatusLine label="CI federation" value={s.federation} />
            </>
          }
          actions={[
            { label: 'Sync →', to: '/operations?tab=sync' },
            { label: 'Federation →', to: '/settings?section=federation' },
          ]}
        />
        <ConnectorCard
          icon={<Boxes size={18} />}
          title="Kubernetes"
          description="Mirror a config's secrets into a namespaced Kubernetes Secret."
          statuses={<StatusLine label="Sync targets" value={s.k8sSync} />}
          actions={[{ label: 'Manage →', to: '/operations?tab=sync' }]}
        />
        <ConnectorCard
          icon={<KeyRound size={18} />}
          title="OIDC (SSO login)"
          description="Let users sign in through your OIDC identity provider."
          statuses={<StatusLine label="Login" value={s.oidcLogin} />}
          actions={[{ label: 'Configure →', to: '/settings?section=oidc' }]}
        />
      </div>
    </div>
  )
}
