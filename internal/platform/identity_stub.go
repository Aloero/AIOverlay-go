//go:build !windows

package platform

func ConfigureProcessIdentity(company, product string) string { return company + "." + product }
