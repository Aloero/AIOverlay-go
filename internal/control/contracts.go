package control

import "aioverlay/internal/contracts"

// Aliases preserve existing Go callers and the JSON wire format.
type CatalogQuery = contracts.CatalogQuery
type CatalogPage = contracts.CatalogPage
type BenchmarkRequest = contracts.BenchmarkRequest
type BenchmarkRun = contracts.BenchmarkRun
type BenchmarkReport = contracts.BenchmarkReport
type TestRequest = contracts.TestRequest
type StudioState = contracts.StudioState
