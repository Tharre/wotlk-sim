package core

import (
	"fmt"
	"math"
	"time"

	"github.com/wowsims/wotlk/sim/core/stats"
)

type ApplySpellEffects func(*Simulation, *Unit, *Spell)

type SpellConfig struct {
	// See definition of Spell (below) for comments on these.
	ActionID
	SpellSchool  SpellSchool
	Flags        SpellFlag
	ResourceType stats.Stat
	BaseCost     float64

	Cast CastConfig

	ApplyEffects ApplySpellEffects

	InitialDamageMultiplier float64
}

// Metric totals for a spell against a specific target, for the current iteration.
type SpellMetrics struct {
	Casts              int32
	Misses             int32
	Hits               int32
	Crits              int32
	Crushes            int32
	Dodges             int32
	Glances            int32
	Parries            int32
	Blocks             int32
	PartialResists_1_4 int32   // 1/4 of the spell was resisted
	PartialResists_2_4 int32   // 2/4 of the spell was resisted
	PartialResists_3_4 int32   // 3/4 of the spell was resisted
	TotalDamage        float64 // Damage done by all casts of this spell.
	TotalThreat        float64 // Threat generated by all casts of this spell.
	TotalHealing       float64 // Healing done by all casts of this spell.
	TotalShielding     float64 // Shielding done by all casts of this spell.
}

type Spell struct {
	// ID for this spell.
	ActionID

	// The unit who will perform this spell.
	Unit *Unit

	// Fire, Frost, Shadow, etc.
	SpellSchool SpellSchool

	// Flags
	Flags SpellFlag

	// Should be stats.Mana, stats.Energy, stats.Rage, or unset.
	ResourceType      stats.Stat
	ResourceMetrics   *ResourceMetrics
	comboPointMetrics *ResourceMetrics
	runicPowerMetrics *ResourceMetrics
	bloodRuneMetrics  *ResourceMetrics
	frostRuneMetrics  *ResourceMetrics
	unholyRuneMetrics *ResourceMetrics
	deathRuneMetrics  *ResourceMetrics
	healthMetrics     []*ResourceMetrics

	// Base cost. Many effects in the game which 'reduce mana cost by X%'
	// are calculated using the base cost.
	BaseCost float64

	// Default cast parameters with all static effects applied.
	DefaultCast Cast

	CD       Cooldown
	SharedCD Cooldown

	// Performs a cast of this spell.
	castFn CastSuccessFunc

	SpellMetrics []SpellMetrics

	ApplyEffects ApplySpellEffects

	// The current or most recent cast data.
	CurCast Cast

	CastTimeMultiplier float64 // For dynamic effects. Do not set during initialization.
	CostMultiplier     float64 // For dynamic effects. Do not set during initialization.
	DamageMultiplier   float64 // For dynamic effects. Do not set during initialization.
	BonusCritRating    float64 // For dynamic effects. Do not set during initialization.

	initialDamageMultiplier float64
}

// Registers a new spell to the unit. Returns the newly created spell.
func (unit *Unit) RegisterSpell(config SpellConfig) *Spell {
	if len(unit.Spellbook) > 100 {
		panic(fmt.Sprintf("Over 100 registered spells when registering %s! There is probably a spell being registered every iteration.", config.ActionID))
	}

	spell := &Spell{
		ActionID:     config.ActionID,
		Unit:         unit,
		SpellSchool:  config.SpellSchool,
		Flags:        config.Flags,
		ResourceType: config.ResourceType,
		BaseCost:     config.BaseCost,

		DefaultCast: config.Cast.DefaultCast,
		CD:          config.Cast.CD,
		SharedCD:    config.Cast.SharedCD,

		ApplyEffects: config.ApplyEffects,

		CastTimeMultiplier: 1,
		CostMultiplier:     1,
		DamageMultiplier:   1,

		initialDamageMultiplier: 1,
	}

	if config.InitialDamageMultiplier != 0 {
		spell.DamageMultiplier = config.InitialDamageMultiplier
		spell.initialDamageMultiplier = config.InitialDamageMultiplier
	}

	switch spell.ResourceType {
	case stats.Mana:
		spell.ResourceMetrics = spell.Unit.NewManaMetrics(spell.ActionID)
	case stats.Rage:
		spell.ResourceMetrics = spell.Unit.NewRageMetrics(spell.ActionID)
	case stats.Energy:
		spell.ResourceMetrics = spell.Unit.NewEnergyMetrics(spell.ActionID)
	case stats.RunicPower:
		spell.ResourceMetrics = spell.Unit.NewRunicPowerMetrics(spell.ActionID)
	case stats.BloodRune:
		spell.ResourceMetrics = spell.Unit.NewBloodRuneMetrics(spell.ActionID)
	case stats.FrostRune:
		spell.ResourceMetrics = spell.Unit.NewFrostRuneMetrics(spell.ActionID)
	case stats.UnholyRune:
		spell.ResourceMetrics = spell.Unit.NewUnholyRuneMetrics(spell.ActionID)
	case stats.DeathRune:
		spell.ResourceMetrics = spell.Unit.NewDeathRuneMetrics(spell.ActionID)
	}

	spell.castFn = spell.makeCastFunc(config.Cast, spell.applyEffects)

	if spell.ApplyEffects == nil {
		spell.ApplyEffects = func(*Simulation, *Unit, *Spell) {}
	}

	unit.Spellbook = append(unit.Spellbook, spell)

	return spell
}

// Returns the first registered spell with the given ID, or nil if there are none.
func (unit *Unit) GetSpell(actionID ActionID) *Spell {
	for _, spell := range unit.Spellbook {
		if spell.ActionID.SameAction(actionID) {
			return spell
		}
	}
	return nil
}

// Retrieves an existing spell with the same ID as the config uses, or registers it if there is none.
func (unit *Unit) GetOrRegisterSpell(config SpellConfig) *Spell {
	registered := unit.GetSpell(config.ActionID)
	if registered == nil {
		return unit.RegisterSpell(config)
	} else {
		return registered
	}
}

// Metrics for the current iteration
func (spell *Spell) CurDamagePerCast() float64 {
	if spell.SpellMetrics[0].Casts == 0 {
		return 0
	} else {
		casts := int32(0)
		damage := 0.0
		for _, opponent := range spell.Unit.GetOpponents() {
			casts += spell.SpellMetrics[opponent.UnitIndex].Casts
			damage += spell.SpellMetrics[opponent.UnitIndex].TotalDamage
		}
		return damage / float64(casts)
	}
}

func (spell *Spell) finalize() {
	// Assert that user doesn't set dynamic fields during static initialization.
	if spell.CastTimeMultiplier != 1 {
		panic(spell.ActionID.String() + " has non-default CastTimeMultiplier during finalize!")
	}
	if spell.CostMultiplier != 1 {
		panic(spell.ActionID.String() + " has non-default CostMultiplier during finalize!")
	}
	// There are now spells which need to set DamageMultiplier at start, for additive effects.
	//if spell.DamageMultiplier != 1 {
	//	panic(spell.ActionID.String() + " has non-default DamageMultiplier during finalize!")
	//}
	if spell.BonusCritRating != 0 {
		panic(spell.ActionID.String() + " has non-default BonusCritRating during finalize!")
	}
}

func (spell *Spell) reset(sim *Simulation) {
	spell.SpellMetrics = make([]SpellMetrics, len(spell.Unit.Env.AllUnits))

	// Reset dynamic effects.
	spell.CastTimeMultiplier = 1
	spell.CostMultiplier = 1
	spell.DamageMultiplier = spell.initialDamageMultiplier
	spell.BonusCritRating = 0
}

func (spell *Spell) doneIteration() {
	if !spell.Flags.Matches(SpellFlagNoMetrics) {
		spell.Unit.Metrics.addSpell(spell)
	}
}

func (spell *Spell) ComboPointMetrics() *ResourceMetrics {
	if spell.comboPointMetrics == nil {
		spell.comboPointMetrics = spell.Unit.NewComboPointMetrics(spell.ActionID)
	}
	return spell.comboPointMetrics
}

func (spell *Spell) RunicPowerMetrics() *ResourceMetrics {
	if spell.runicPowerMetrics == nil {
		spell.runicPowerMetrics = spell.Unit.NewRunicPowerMetrics(spell.ActionID)
	}
	return spell.runicPowerMetrics
}

func (spell *Spell) BloodRuneMetrics() *ResourceMetrics {
	if spell.bloodRuneMetrics == nil {
		spell.bloodRuneMetrics = spell.Unit.NewBloodRuneMetrics(spell.ActionID)
	}
	return spell.bloodRuneMetrics
}

func (spell *Spell) FrostRuneMetrics() *ResourceMetrics {
	if spell.frostRuneMetrics == nil {
		spell.frostRuneMetrics = spell.Unit.NewFrostRuneMetrics(spell.ActionID)
	}
	return spell.frostRuneMetrics
}

func (spell *Spell) UnholyRuneMetrics() *ResourceMetrics {
	if spell.unholyRuneMetrics == nil {
		spell.unholyRuneMetrics = spell.Unit.NewUnholyRuneMetrics(spell.ActionID)
	}
	return spell.unholyRuneMetrics
}

func (spell *Spell) DeathRuneMetrics() *ResourceMetrics {
	if spell.deathRuneMetrics == nil {
		spell.deathRuneMetrics = spell.Unit.NewDeathRuneMetrics(spell.ActionID)
	}
	return spell.deathRuneMetrics
}

func (spell *Spell) HealthMetrics(target *Unit) *ResourceMetrics {
	if spell.healthMetrics == nil {
		spell.healthMetrics = make([]*ResourceMetrics, len(spell.Unit.AttackTables))
	}
	if spell.healthMetrics[target.UnitIndex] == nil {
		spell.healthMetrics[target.UnitIndex] = target.NewHealthMetrics(spell.ActionID)
	}
	return spell.healthMetrics[target.UnitIndex]
}

func (spell *Spell) ReadyAt() time.Duration {
	return BothTimersReadyAt(spell.CD.Timer, spell.SharedCD.Timer)
}

func (spell *Spell) IsReady(sim *Simulation) bool {
	if spell == nil {
		return false
	}
	return BothTimersReady(spell.CD.Timer, spell.SharedCD.Timer, sim)
}

func (spell *Spell) TimeToReady(sim *Simulation) time.Duration {
	return MaxTimeToReady(spell.CD.Timer, spell.SharedCD.Timer, sim)
}

func (spell *Spell) Cast(sim *Simulation, target *Unit) bool {
	return spell.castFn(sim, target)
}

// Skips the actual cast and applies spell effects immediately.
func (spell *Spell) SkipCastAndApplyEffects(sim *Simulation, target *Unit) {
	if sim.Log != nil && !spell.Flags.Matches(SpellFlagNoLogs) {
		spell.Unit.Log(sim, "Casting %s (Cost = %0.03f, Cast Time = %s)",
			spell.ActionID, spell.DefaultCast.Cost, time.Duration(0))
		spell.Unit.Log(sim, "Completed cast %s", spell.ActionID)
	}
	spell.applyEffects(sim, target)
}

func (spell *Spell) applyEffects(sim *Simulation, target *Unit) {
	if spell.SpellMetrics == nil {
		spell.reset(sim)
	}
	if target == nil {
		target = spell.Unit.CurrentTarget
	}
	spell.SpellMetrics[target.UnitIndex].Casts++
	spell.ApplyEffects(sim, target, spell)
}

func (spell *Spell) ApplyAOEThreatIgnoreMultipliers(threatAmount float64) {
	numTargets := spell.Unit.Env.GetNumTargets()
	for i := int32(0); i < numTargets; i++ {
		spell.SpellMetrics[i].TotalThreat += threatAmount
	}
}
func (spell *Spell) ApplyAOEThreat(threatAmount float64) {
	spell.ApplyAOEThreatIgnoreMultipliers(threatAmount * spell.TotalThreatMultiplier())
}

func ApplyEffectFuncDirectDamage(baseEffect SpellEffect) ApplySpellEffects {
	baseEffect.Validate()
	if baseEffect.BaseDamage.Calculator == nil {
		// Just a hit check.
		return func(sim *Simulation, target *Unit, spell *Spell) {
			effect := &baseEffect
			effect.Target = target
			attackTable := spell.Unit.AttackTables[target.UnitIndex]
			effect.init(sim, spell)

			effect.OutcomeApplier(sim, spell, effect, attackTable)
			effect.finalize(sim, spell)
		}
	} else {
		return func(sim *Simulation, target *Unit, spell *Spell) {
			effect := &baseEffect
			effect.Target = target
			attackTable := spell.Unit.AttackTables[target.UnitIndex]
			effect.init(sim, spell)

			effect.Damage = effect.calculateBaseDamage(sim, spell)
			effect.calcDamageSingle(sim, spell, attackTable)
			effect.finalize(sim, spell)
		}
	}
}

func ApplyEffectFuncDirectDamageTargetModifiersOnly(baseEffect SpellEffect) ApplySpellEffects {
	baseEffect.Validate()
	return func(sim *Simulation, target *Unit, spell *Spell) {
		effect := &baseEffect
		effect.Target = target
		attackTable := spell.Unit.AttackTables[target.UnitIndex]

		effect.Damage = effect.calculateBaseDamage(sim, spell)
		effect.calcDamageTargetOnly(sim, spell, attackTable)
		effect.finalize(sim, spell)
	}
}

func ApplyEffectFuncDamageMultiple(baseEffects []SpellEffect) ApplySpellEffects {
	for _, effect := range baseEffects {
		effect.Validate()
	}

	if len(baseEffects) == 0 {
		panic("Multiple damage requires hits")
	} else if len(baseEffects) == 1 {
		return ApplyEffectFuncDirectDamage(baseEffects[0])
	}

	return func(sim *Simulation, _ *Unit, spell *Spell) {
		for i := range baseEffects {
			effect := &baseEffects[i]
			effect.init(sim, spell)
			effect.Damage = effect.calculateBaseDamage(sim, spell)
			attackTable := spell.Unit.AttackTables[effect.Target.UnitIndex]
			effect.calcDamageSingle(sim, spell, attackTable)
		}
		for i := range baseEffects {
			effect := &baseEffects[i]
			effect.finalize(sim, spell)
		}
	}
}
func ApplyEffectFuncDamageMultipleTargeted(baseEffects []SpellEffect) ApplySpellEffects {
	for _, effect := range baseEffects {
		effect.Validate()
	}

	if len(baseEffects) == 0 {
		panic("Multiple damage requires hits")
	} else if len(baseEffects) == 1 {
		return ApplyEffectFuncDirectDamage(baseEffects[0])
	}

	return func(sim *Simulation, target *Unit, spell *Spell) {
		for i := range baseEffects {
			effect := &baseEffects[i]
			effect.Target = target
			attackTable := spell.Unit.AttackTables[target.UnitIndex]
			effect.init(sim, spell)
			effect.Damage = effect.calculateBaseDamage(sim, spell)
			effect.calcDamageSingle(sim, spell, attackTable)
		}
		for i := range baseEffects {
			effect := &baseEffects[i]
			effect.finalize(sim, spell)
		}
	}
}
func ApplyEffectFuncAOEDamage(env *Environment, baseEffect SpellEffect) ApplySpellEffects {
	baseEffect.Validate()
	numHits := env.GetNumTargets()
	effects := make([]SpellEffect, numHits)
	for i := int32(0); i < numHits; i++ {
		effects[i] = baseEffect
		effects[i].Target = &env.GetTarget(i).Unit
	}
	return ApplyEffectFuncDamageMultiple(effects)
}

func ApplyEffectFuncDot(dot *Dot) ApplySpellEffects {
	return func(sim *Simulation, _ *Unit, _ *Spell) {
		dot.Apply(sim)
	}
}

// AOE Cap Mechanics:
// http://web.archive.org/web/20081023033855/http://elitistjerks.com/f47/t25902-aoe_spell_cap_mechanics/
func applyAOECap(effects []SpellEffect, outcomeMultipliers []float64, aoeCap float64) {
	// Increased damage from crits doesn't count towards the cap, so need to
	// tally pre-crit damage.
	totalTowardsCap := 0.0
	for i, _ := range effects {
		effect := &effects[i]
		if effect.Outcome.Matches(OutcomeCrit) {
			totalTowardsCap += effect.Damage / outcomeMultipliers[i]
		} else {
			totalTowardsCap += effect.Damage
		}
	}

	if totalTowardsCap <= aoeCap {
		return
	}

	capMultiplier := aoeCap / totalTowardsCap
	for i, _ := range effects {
		effect := &effects[i]
		effect.Damage *= capMultiplier
	}
}
func ApplyEffectFuncAOEDamageCapped(env *Environment, baseEffect SpellEffect) ApplySpellEffects {
	baseEffect.Validate()
	numHits := env.GetNumTargets()
	if numHits == 0 {
		return nil
	} else if numHits == 1 {
		return ApplyEffectFuncDirectDamage(baseEffect)
	} else if numHits < 4 {
		// Just assume its impossible to hit AOE cap with <4 targets.
		return ApplyEffectFuncAOEDamage(env, baseEffect)
	}

	baseEffects := make([]SpellEffect, numHits)
	for i := int32(0); i < numHits; i++ {
		baseEffects[i] = baseEffect
		baseEffects[i].Target = &env.GetTarget(i).Unit
	}
	return ApplyEffectFuncMultipleDamageCapped(baseEffects, false)
}

func ApplyEffectFuncAOEDamageCappedWithDeferredEffects(env *Environment, baseEffect SpellEffect) ApplySpellEffects {
	baseEffect.Validate()
	numHits := env.GetNumTargets()
	if numHits == 0 {
		return nil
	}
	baseEffects := make([]SpellEffect, numHits)
	for i := int32(0); i < numHits; i++ {
		baseEffects[i] = baseEffect
		baseEffects[i].Target = &env.GetTarget(i).Unit
	}
	return ApplyEffectFuncMultipleDamageCapped(baseEffects, true)
}

func ApplyEffectFuncMultipleDamageCapped(baseEffects []SpellEffect, deferFinalization bool) ApplySpellEffects {
	numTargets := len(baseEffects)
	for _, effect := range baseEffects {
		effect.Validate()
	}

	return func(sim *Simulation, _ *Unit, spell *Spell) {
		capMultiplier := math.Min(10.0/float64(numTargets), 1.0)
		for i := range baseEffects {
			effect := &baseEffects[i]
			effect.init(sim, spell)
			attackTable := spell.Unit.AttackTables[effect.Target.UnitIndex]
			effect.Damage = effect.calculateBaseDamage(sim, spell)
			effect.Damage *= capMultiplier
			effect.applyAttackerModifiers(sim, spell)
			effect.applyResistances(sim, spell, attackTable)
			effect.OutcomeApplier(sim, spell, effect, attackTable)
			effect.applyTargetModifiers(sim, spell, attackTable)
			if !deferFinalization {
				effect.finalize(sim, spell)
			}
		}
		if deferFinalization {
			for i := range baseEffects {
				effect := &baseEffects[i]
				effect.finalize(sim, spell)
			}
		}
	}
}

func ApplyEffectFuncWithOutcome(baseEffects []SpellEffect, onOutcome func(*Simulation, HitOutcome)) ApplySpellEffects {
	if len(baseEffects) == 1 {
		baseEffects[0].Validate()
		if baseEffects[0].BaseDamage.Calculator == nil {
			// Just a hit check.
			return func(sim *Simulation, target *Unit, spell *Spell) {
				effect := &baseEffects[0]
				effect.Target = target
				attackTable := spell.Unit.AttackTables[target.UnitIndex]
				effect.init(sim, spell)
				effect.OutcomeApplier(sim, spell, effect, attackTable)
				onOutcome(sim, effect.Outcome)
				effect.finalize(sim, spell)
			}
		} else {
			return func(sim *Simulation, target *Unit, spell *Spell) {
				effect := &baseEffects[0]
				effect.Target = target
				attackTable := spell.Unit.AttackTables[target.UnitIndex]
				effect.init(sim, spell)
				effect.Damage = effect.calculateBaseDamage(sim, spell)
				effect.calcDamageSingle(sim, spell, attackTable)
				onOutcome(sim, effect.Outcome)
				effect.finalize(sim, spell)
			}
		}
	} else {
		for _, effect := range baseEffects {
			effect.Validate()
		}
		return func(sim *Simulation, _ *Unit, spell *Spell) {
			capMultiplier := math.Min(10.0/float64(len(baseEffects)), 1.0)
			for i := range baseEffects {
				effect := &baseEffects[i]
				effect.init(sim, spell)
				attackTable := spell.Unit.AttackTables[effect.Target.UnitIndex]
				effect.Damage = effect.calculateBaseDamage(sim, spell)
				effect.Damage *= capMultiplier
				effect.applyAttackerModifiers(sim, spell)
				effect.applyResistances(sim, spell, attackTable)
				effect.OutcomeApplier(sim, spell, effect, attackTable)
				if i == 0 {
					onOutcome(sim, effect.Outcome)
				}
				effect.applyTargetModifiers(sim, spell, attackTable)
				effect.finalize(sim, spell)
			}
		}
	}
}

var ApplyEffectFuncDirectHealing = ApplyEffectFuncDirectDamage
