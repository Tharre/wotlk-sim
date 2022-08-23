package druid

import (
	"github.com/wowsims/wotlk/sim/core"
	"github.com/wowsims/wotlk/sim/core/items"
	"github.com/wowsims/wotlk/sim/core/stats"
)

func (druid *Druid) registerSwipeSpell() {
	cost := 20.0 - float64(druid.Talents.Ferocity)

	baseDamage := 108.0
	if druid.Equip[items.ItemSlotRanged].ID == 23198 { // Idol of Brutality
		baseDamage += 10
	}

	lbdm := core.TernaryFloat64(druid.HasSetBonus(ItemSetLasherweaveBattlegear, 2), 1.2, 1.0)
	thdm := core.TernaryFloat64(druid.HasSetBonus(ItemSetThunderheartHarness, 4), 1.15, 1.0)

	baseEffect := core.SpellEffect{
		ProcMask: core.ProcMaskMeleeMHSpecial,

		DamageMultiplier: lbdm * thdm,
		ThreatMultiplier: 1,

		BaseDamage: core.BaseDamageConfig{
			Calculator: func(sim *core.Simulation, hitEffect *core.SpellEffect, spell *core.Spell) float64 {
				return baseDamage + 0.07*hitEffect.MeleeAttackPower(spell.Unit)
			},
			TargetSpellCoefficient: 1,
		},
		OutcomeApplier: druid.OutcomeFuncMeleeSpecialHitAndCrit(druid.MeleeCritMultiplier()),
	}

	numHits := core.MinInt32(3, druid.Env.GetNumTargets())
	effects := make([]core.SpellEffect, 0, numHits)
	for i := int32(0); i < numHits; i++ {
		effect := baseEffect
		effect.Target = druid.Env.GetTargetUnit(i)
		effects = append(effects, effect)
	}

	druid.Swipe = druid.RegisterSpell(core.SpellConfig{
		ActionID:    core.ActionID{SpellID: 48562},
		SpellSchool: core.SpellSchoolPhysical,
		Flags:       core.SpellFlagMeleeMetrics,

		ResourceType: stats.Rage,
		BaseCost:     cost,

		Cast: core.CastConfig{
			DefaultCast: core.Cast{
				Cost: cost,
				GCD:  core.GCDDefault,
			},
			ModifyCast:  druid.ApplyClearcasting,
			IgnoreHaste: true,
		},

		ApplyEffects: core.ApplyEffectFuncDamageMultiple(effects),
	})
}

func (druid *Druid) CanSwipe() bool {
	return druid.InForm(Bear) && druid.CurrentRage() >= druid.Swipe.DefaultCast.Cost
}
