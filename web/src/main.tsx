import '@fontsource/jetbrains-mono/400.css'
import '@fontsource/jetbrains-mono/500.css'
import '@fontsource/silkscreen/400.css'
import './styles.css'
import './lib/theme' // applies the saved theme before the first render

import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { RouterProvider } from '@tanstack/react-router'
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { metaQuery } from './lib/api'
import { makeRouter } from './router'

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: false, refetchOnWindowFocus: false } },
})
const router = makeRouter(queryClient)

// The product name comes from the server; nothing in this app hard-codes it.
queryClient
  .ensureQueryData(metaQuery)
  .then((meta) => (document.title = meta.name))
  .catch(() => {})

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <RouterProvider router={router} />
    </QueryClientProvider>
  </StrictMode>,
)
