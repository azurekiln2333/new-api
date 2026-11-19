# Sidebar groups

Compose sidebar groups through the existing navigation data and `NavCollapsible`.
The Logs group has four independent leaves: usage (`/usage-logs/common`), audit,
drawing and task. Preserve their original permission and module identifiers.
Do not alias drawing to the task route for highlighting or module visibility.
The existing recursive module filter removes hidden leaves and an empty parent.

Cover desktop/mobile keyboard expansion, active child links, collapsed-sidebar
menus, independent module switches and empty groups in the existing
`web/src/hooks/__tests__/sidebar-config.test.tsx` suite.
