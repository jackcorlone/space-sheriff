package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"regexp"
	"unicode/utf8"
)

type Policy struct {
	ID                      string `json:"id"`
	Name                    string `json:"name"`
	Version                 int    `json:"version"`
	Description             string `json:"description"`
	InstallerMinAgeDays     int    `json:"installerMinAgeDays"`
	CacheMinAgeDays         int    `json:"cacheMinAgeDays"`
	CacheHighConfidenceDays int    `json:"cacheHighConfidenceDays"`
	ArchiveMinAgeDays       int    `json:"archiveMinAgeDays"`
	LargeStaleMinAgeDays    int    `json:"largeStaleMinAgeDays"`
	LargeStaleMinBytes      int64  `json:"largeStaleMinBytes"`
	BuiltIn                 bool   `json:"builtIn"`
}

var policyIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

var builtInPolicies = []Policy{
	{
		ID: "conservative", Name: "稳健", Version: 1,
		Description:             "适合生产机和重要工作站，提高所有可清理阈值。",
		InstallerMinAgeDays:     60,
		CacheMinAgeDays:         30,
		CacheHighConfidenceDays: 90,
		ArchiveMinAgeDays:       180,
		LargeStaleMinAgeDays:    365,
		LargeStaleMinBytes:      20 * 1024 * 1024 * 1024,
		BuiltIn:                 true,
	},
	{
		ID: "balanced", Name: "均衡", Version: 1,
		Description:             "兼顾安全与空间回收，保持 v0.4 的默认判断。",
		InstallerMinAgeDays:     30,
		CacheMinAgeDays:         7,
		CacheHighConfidenceDays: 30,
		ArchiveMinAgeDays:       90,
		LargeStaleMinAgeDays:    180,
		LargeStaleMinBytes:      10 * 1024 * 1024 * 1024,
		BuiltIn:                 true,
	},
	{
		ID: "reclaim-focused", Name: "空间优先", Version: 1,
		Description:             "适合空间紧张时使用；仍需人工确认且不会放宽系统保护。",
		InstallerMinAgeDays:     14,
		CacheMinAgeDays:         3,
		CacheHighConfidenceDays: 14,
		ArchiveMinAgeDays:       60,
		LargeStaleMinAgeDays:    90,
		LargeStaleMinBytes:      5 * 1024 * 1024 * 1024,
		BuiltIn:                 true,
	},
}

func balancedPolicy() Policy {
	return builtInPolicies[1]
}

func validatePolicy(policy Policy) error {
	if !policyIDPattern.MatchString(policy.ID) {
		return fmt.Errorf("策略 ID 必须由小写字母、数字和连字符组成，长度为 1 到 64")
	}
	if utf8.RuneCountInString(policy.Name) < 1 || utf8.RuneCountInString(policy.Name) > 80 {
		return fmt.Errorf("策略名称长度必须为 1 到 80 个字符")
	}
	if utf8.RuneCountInString(policy.Description) > 300 {
		return fmt.Errorf("策略说明不能超过 300 个字符")
	}
	if policy.Version < 1 || policy.Version > 1_000_000 {
		return fmt.Errorf("策略版本必须在 1 到 1000000 之间")
	}
	for name, days := range map[string]int{
		"installerMinAgeDays":     policy.InstallerMinAgeDays,
		"cacheMinAgeDays":         policy.CacheMinAgeDays,
		"cacheHighConfidenceDays": policy.CacheHighConfidenceDays,
		"archiveMinAgeDays":       policy.ArchiveMinAgeDays,
		"largeStaleMinAgeDays":    policy.LargeStaleMinAgeDays,
	} {
		if days < 1 || days > 3650 {
			return fmt.Errorf("%s 必须在 1 到 3650 之间", name)
		}
	}
	if policy.CacheHighConfidenceDays < policy.CacheMinAgeDays {
		return fmt.Errorf("cacheHighConfidenceDays 不能小于 cacheMinAgeDays")
	}
	if policy.LargeStaleMinBytes < 1024*1024*1024 ||
		policy.LargeStaleMinBytes > 100*1024*1024*1024*1024 {
		return fmt.Errorf("largeStaleMinBytes 必须在 1 GiB 到 100 TiB 之间")
	}
	return nil
}

func decodePolicy(data []byte) (Policy, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var policy Policy
	if err := decoder.Decode(&policy); err != nil {
		return Policy{}, fmt.Errorf("策略 JSON 无效: %w", err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return Policy{}, fmt.Errorf("策略 JSON 只能包含一个对象")
	}
	if policy.BuiltIn {
		return Policy{}, fmt.Errorf("导入策略不能声明为内置策略")
	}
	if err := validatePolicy(policy); err != nil {
		return Policy{}, err
	}
	return policy, nil
}
