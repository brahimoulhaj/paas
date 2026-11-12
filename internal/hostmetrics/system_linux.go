package hostmetrics

import "context"

func readSystem(ctx context.Context, options Options) reading { return readLinuxSystem(ctx, options) }
