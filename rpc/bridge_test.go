package rpc

import (
	"context"
	"reflect"
	"testing"

	sp "github.com/wxy365/sprout"
)

// bridgeInput 是跨包校验复用用的入参结构体。
type bridgeInput struct {
	Name string `validate:"required"`
}

// TestCorePackageExportsTheValidatorBuilder 验证核心包导出的校验构建函数
// 可以被 rpc 包复用，从而让 gRPC 入参沿用同一套 validate 标签规则。
func TestCorePackageExportsTheValidatorBuilder(t *testing.T) {
	fn := sp.BuildInputEntityValidateFuncs(sp.DefaultValidators(), reflect.TypeOf(bridgeInput{}))
	if fn == nil {
		t.Fatalf("the validator function should not be nil")
	}
	var in bridgeInput
	if err := fn(context.Background(), reflect.ValueOf(&in).Elem()); err == nil {
		t.Errorf("an empty required field should fail the validation")
	}
	in.Name = "wxy"
	if err := fn(context.Background(), reflect.ValueOf(&in).Elem()); err != nil {
		t.Errorf("a valid input should pass the validation, got %v", err)
	}
}

// TestCorePackageExportsTheGuards 验证核心包导出的熔断与限流守卫可以被 rpc 包复用。
func TestCorePackageExportsTheGuards(t *testing.T) {
	cb := sp.NewCircuitBreakerGuard("bridge-test")
	if cb == nil {
		t.Fatalf("the circuit breaker guard should not be nil")
	}
	done, err := cb.Allow()
	if err != nil {
		t.Fatalf("the first call should be allowed, got %v", err)
	}
	done(true)

	rl := sp.NewRateLimiterGuard("bridge-test")
	if rl == nil {
		t.Fatalf("the rate limiter guard should not be nil")
	}
	if !rl.Allow("") {
		t.Errorf("the first call should be allowed")
	}
}
