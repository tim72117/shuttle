import { useEffect, useState } from 'react'
import type { ApiCall } from './api'
import { onApiCall } from './api'
import { useAppState, PhoneContent } from './App'
import { DebugPanel } from './DebugPanel'
import type { User } from './types'
import './debug.css'

function StatusBar({ user }: { user: User | null }) {
  return (
    <div className="statusbar">
      <span>9:41</span>
      <span>{user ? user.name : ''} 📶 🔋</span>
    </div>
  )
}

export function DebugApp() {
  const props = useAppState()
  const [calls, setCalls] = useState<ApiCall[]>([])
  useEffect(() => onApiCall((c) => setCalls((prev) => [c, ...prev].slice(0, 100))), [])

  return (
    <div className="workbench">
      <div className="phone">
        <div className="phone-screen">
          <div className="notch" />
          <StatusBar user={props.user} />
          <PhoneContent {...props} />
        </div>
      </div>
      <DebugPanel
        calls={calls}
        onClear={() => setCalls([])}
        cfg={props.cfg}
        channel={props.activeChannel}
      />
    </div>
  )
}
