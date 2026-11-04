package telemetry

import "errors"

// otlpOptionFuncs は、OTLP exporter の共通設定（insecure、エンドポイント、gzip、ヘッダ）を
// シグナル・プロトコル固有のオプション型 O に変換する関数の組。
// otlploghttp / otlpmetricgrpc などはシグナルごとにオプション型が別なので、
// 設定の解釈は build に集約し、各 exporter はこの組を渡すだけにする。
type otlpOptionFuncs[O any] struct {
	// insecure は gRPC のみが持つ。HTTP では nil のままにする。
	insecure    func() O
	endpoint    func(string) O
	endpointURL func(string) O
	gzip        func() O
	headers     func(map[string]string) O
}

// build は設定値からオプションを組み立てる。順序は insecure、エンドポイント、gzip、ヘッダ。
// エンドポイントは Endpoint（addr）を EndpointURL（url）より優先し、どちらも無ければエラーにする。
func (f otlpOptionFuncs[O]) build(endpoint *Endpoint, insecure, gzipCompression bool) ([]O, error) {
	opts := make([]O, 0, 4)
	if insecure && f.insecure != nil {
		opts = append(opts, f.insecure())
	}
	switch {
	case endpoint.Endpoint != "":
		opts = append(opts, f.endpoint(endpoint.Endpoint))
	case endpoint.EndpointURL != "":
		opts = append(opts, f.endpointURL(endpoint.EndpointURL))
	default:
		return nil, errors.New("please specify the endpoint or endpoint URL")
	}
	if gzipCompression {
		opts = append(opts, f.gzip())
	}
	if len(endpoint.Headers) > 0 {
		opts = append(opts, f.headers(endpoint.Headers))
	}
	return opts, nil
}

// buildHTTP は HTTP エクスポータ向けにオプションを組み立てる（insecure は無い）。
func (f otlpOptionFuncs[O]) buildHTTP(h *HTTP, gzipCompression bool) ([]O, error) {
	return f.build(&h.Endpoint, false, gzipCompression)
}

// buildGRPC は gRPC エクスポータ向けにオプションを組み立てる。
func (f otlpOptionFuncs[O]) buildGRPC(g *GRPC, gzipCompression bool) ([]O, error) {
	return f.build(&g.Endpoint, g.Insecure, gzipCompression)
}
