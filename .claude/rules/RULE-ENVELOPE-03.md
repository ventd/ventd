# RULE-ENVELOPE-03: ClassThresholds lookup returns the correct Thresholds struct for every SystemClass including ClassUnknown.

`LookupThresholds(cls sysclass.SystemClass) Thresholds` MUST return the canonical `Thresholds`
for each of the seven non-Unknown classes. For `ClassUnknown` (or any unrecognized class value),
it MUST return the `ClassMidDesktop` thresholds as the safe default — MidDesktop is the
statistically most common class and its thresholds are the most conservative of the consumer
desktop classes. A missing or zero-value Thresholds for any class would cause division by zero
(SampleHz=0) or an empty PWMSteps slice that produces no probe writes.

Bound: internal/envelope/envelope_test.go:TestRULE_ENVELOPE_03_ClassThresholdLookup
