package hivemindassets

import "embed"

// DashboardDistFS embeds the frontend build artifacts produced in dashboard/dist.
//
//go:embed dashboard/dist dashboard/dist/*
var DashboardDistFS embed.FS
