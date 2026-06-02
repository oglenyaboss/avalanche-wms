import { createBrowserRouter, RouterProvider } from 'react-router'

import { GuestOnlyRoute } from '@/app/providers/GuestOnlyRoute'
import { ProtectedRoute } from '@/app/providers/ProtectedRoute'
import { AnalyticsPage } from '@/pages/analytics'
import { AssemblyPage } from '@/pages/assembly'
import { HomePage } from '@/pages/home'
import { LoginPage } from '@/pages/login'
import { NotFoundPage } from '@/pages/not-found'
import { PutawayPage } from '@/pages/putaway'
import { ReceivingGatePage } from '@/pages/receiving-gate'
import { ReceivingTablePage } from '@/pages/receiving-table'
import { ShippingPage } from '@/pages/shipping'
import { routes } from '@/shared/config/routes'
import { AppShell } from '@/widgets/app-shell'

const router = createBrowserRouter([
  {
    element: <GuestOnlyRoute />,
    children: [
      {
        path: routes.login,
        element: <LoginPage />,
      },
    ],
  },
  {
    element: <ProtectedRoute />,
    children: [
      {
        element: <AppShell />,
        children: [
          {
            path: routes.home,
            element: <HomePage />,
          },
          {
            path: routes.receivingGate,
            element: <ReceivingGatePage />,
          },
          {
            path: routes.receivingTable,
            element: <ReceivingTablePage />,
          },
          {
            path: routes.putaway,
            element: <PutawayPage />,
          },
          {
            path: routes.assembly,
            element: <AssemblyPage />,
          },
          {
            path: routes.shipping,
            element: <ShippingPage />,
          },
          {
            path: routes.analytics,
            element: <AnalyticsPage />,
          },
        ],
      },
    ],
  },
  {
    path: routes.notFound,
    element: <NotFoundPage />,
  },
])

export function AppRouter() {
  return <RouterProvider router={router} />
}
