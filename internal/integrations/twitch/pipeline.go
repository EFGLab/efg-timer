package twitch

import (
	"context"
	"time"

	"github.com/EFGLab/efg-timer/internal/rules"
)

// RunSubs — склейка e04s02-t4: жизненный цикл EventSub (подписки после
// welcome входят в Run) → секунды из правила (снапшот; main перезапускает
// поток при смене правил) → sink (main добавляет секунды таймеру). Ошибки
// начисления сообщаются в onError (nil — молча). Блокируется до отмены ctx.
func RunSubs(ctx context.Context, opts EventSubOptions, rule rules.SubRule, sink func(time.Duration), onError func(error)) error {
	opts.OnSub = func(ev SubEvent) {
		d, err := SubSeconds(rule, ev.Tier)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		if d > 0 && sink != nil {
			sink(d)
		}
	}
	opts.OnResub = func(ev ResubEvent) {
		d, err := SubSeconds(rule, ev.Tier)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		if d > 0 && sink != nil {
			sink(d)
		}
	}
	return NewEventSub(opts).Run(ctx)
}
