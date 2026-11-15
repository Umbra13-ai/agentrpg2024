package game

// InvocationRepeat is an Eldritch Invocation the SRD 5.2.1 lets a Warlock gain more than once ("Repeatable. You can gain this
// invocation more than once. Each time you do so, choose a different ..."), and what each copy chooses. It is the one place the
// server knows which invocations repeat: the invocation request, its options and the effects read it.
type InvocationRepeat struct {
	Choice     string // "cantrip": each copy names a different Warlock cantrip; "origin_feat": each copy gives a different Origin feat
	Damage     bool   // the cantrip deals damage
	AttackRoll bool   // the cantrip requires an attack roll
	MinRange   int    // the cantrip's range is at least this many feet
	Needs      string // the prerequisite in words, for options and refusals
}

// InvocationRepeats: the repeatable invocations of the SRD 5.2.1 Eldritch Invocation Options.
var InvocationRepeats = map[string]InvocationRepeat{
	"agonizing-blast":           {Choice: "cantrip", Damage: true, Needs: "a Warlock cantrip you know that deals damage"},
	"eldritch-spear":            {Choice: "cantrip", Damage: true, MinRange: 10, Needs: "a Warlock cantrip you know that deals damage and has a range of 10+ feet"},
	"repelling-blast":           {Choice: "cantrip", Damage: true, AttackRoll: true, Needs: "a Warlock cantrip you know that requires an attack roll"},
	"lessons-of-the-first-ones": {Choice: "origin_feat", Needs: "a different Origin feat each time"},
}
