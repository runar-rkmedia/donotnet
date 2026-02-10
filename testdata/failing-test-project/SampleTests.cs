using Xunit;

namespace FailingTests;

public class SampleTests
{
    /// <summary>
    /// This test verifies that the calculation returns the expected result.
    /// It deliberately fails to test the docstring extraction feature.
    /// </summary>
    [Fact]
    public void CalculationTest_ShouldReturnCorrectValue()
    {
        var expected = 42;
        var actual = 41; // Wrong value to cause failure

        Assert.Equal(expected, actual);
    }

    /// <summary>
    /// Validates that user authentication works correctly with valid credentials.
    /// This is a multi-line docstring example that should be extracted
    /// and displayed when the test fails.
    /// </summary>
    [Fact]
    public void Authentication_WithValidCredentials_ShouldSucceed()
    {
        var isAuthenticated = false; // Simulating failure

        Assert.True(isAuthenticated, "User should be authenticated with valid credentials");
    }

    /// <summary>
    /// This test passes and should not show its docstring.
    /// </summary>
    [Fact]
    public void PassingTest_ShouldWork()
    {
        Assert.True(true);
    }
}
