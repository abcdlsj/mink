import { create } from 'zustand'

interface UIState {
  sidebarCollapsed: boolean
  contextVisible: boolean
  toggleSidebar: () => void
  toggleContext: () => void
}

export const useUIStore = create<UIState>((set) => ({
  sidebarCollapsed: false,
  contextVisible: true,
  toggleSidebar: () => set((s) => ({ sidebarCollapsed: !s.sidebarCollapsed })),
  toggleContext: () => set((s) => ({ contextVisible: !s.contextVisible })),
}))
